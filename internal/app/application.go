package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/Xmandon/xdom/internal/faults"
	"github.com/Xmandon/xdom/internal/httpapi"
	"github.com/Xmandon/xdom/internal/order"
	"github.com/Xmandon/xdom/internal/payment"
	"github.com/Xmandon/xdom/internal/repository"
	"github.com/Xmandon/xdom/internal/telemetry"
	"github.com/Xmandon/xdom/internal/worker"
)

type Application struct {
	cfg       Config
	server    *http.Server
	worker    *worker.Runner
	telemetry *telemetry.Manager
}

func New(cfg Config) (*Application, error) {
	ctx := context.Background()

	tel, err := telemetry.NewManager(ctx, telemetry.Config{
		ServiceName:       cfg.ServiceName,
		Environment:       cfg.Environment,
		Version:           cfg.Version,
		CommitSHA:         cfg.CommitSHA,
		BuildID:           cfg.BuildID,
		Token:             cfg.Token,
		OTLPEndpoint:      cfg.OTLPEndpoint,
		OTLPInsecure:      cfg.OTLPInsecure,
		EnableTraces:      cfg.EnableTraces,
		EnableMetrics:     cfg.EnableMetrics,
		EnableLogs:        cfg.EnableLogs,
		ExportInterval:    time.Duration(cfg.OTLPExportIntervalSec) * time.Second,
		ExportTimeout:     time.Duration(cfg.OTLPExportTimeoutMS) * time.Millisecond,
		TraceBatchTimeout: time.Duration(cfg.OTLPTraceBatchTimeoutMS) * time.Millisecond,
		LogBatchTimeout:   time.Duration(cfg.OTLPLogBatchTimeoutMS) * time.Millisecond,
		NetHostIP:         cfg.NetHostIP,
	})
	if err != nil {
		return nil, fmt.Errorf("init telemetry: %w", err)
	}

	faultState := faults.NewState()
	repo, err := repository.NewSQLiteRepository(ctx, repository.Config{
		DBPath:  cfg.DBPath,
		Logger:  tel.Logger(),
		Faults:  faultState,
		Tracer:  tel.Tracer(),
		Metrics: tel,
	})
	if err != nil {
		_ = tel.Shutdown(ctx)
		return nil, fmt.Errorf("init repository: %w", err)
	}

	if err := repo.Init(ctx); err != nil {
		_ = tel.Shutdown(ctx)
		return nil, fmt.Errorf("init schema: %w", err)
	}
	if err := repo.SeedInventory(ctx); err != nil {
		_ = tel.Shutdown(ctx)
		return nil, fmt.Errorf("seed inventory: %w", err)
	}

	paymentClient := payment.NewClient(payment.Config{
		BaseLatencyMS: cfg.PaymentLatencyMS,
		Logger:        tel.Logger(),
		Faults:        faultState,
		Tracer:        tel.Tracer(),
		Metrics:       tel,
	})

	orderService := order.NewService(order.Config{
		ServiceName:     cfg.ServiceName,
		Environment:     cfg.Environment,
		Version:         cfg.Version,
		CommitSHA:       cfg.CommitSHA,
		OrderTimeoutSec: cfg.OrderTimeoutSec,
		Repository:      repo,
		PaymentClient:   paymentClient,
		Faults:          faultState,
		Logger:          tel.Logger(),
		Tracer:          tel.Tracer(),
		Metrics:         tel,
	})

	tel.SetActivePendingOrdersProvider(func(ctx context.Context) int64 {
		count, err := repo.CountActivePendingOrders(ctx)
		if err != nil {
			return 0
		}
		return count
	})

	workerRunner := worker.NewRunner(worker.Config{
		Interval:             time.Duration(cfg.WorkerIntervalSec) * time.Second,
		HeartbeatLogInterval: time.Duration(cfg.HeartbeatLogIntervalSec) * time.Second,
		Service:              orderService,
		Logger:               tel.Logger(),
		Faults:               faultState,
		Metrics:              tel,
		Tracer:               tel.Tracer(),
	})

	handler := httpapi.NewHandler(httpapi.Config{
		ServiceName:   cfg.ServiceName,
		Environment:   cfg.Environment,
		Version:       cfg.Version,
		CommitSHA:     cfg.CommitSHA,
		BuildID:       cfg.BuildID,
		AdminToken:    cfg.AdminToken,
		EnableTraces:  cfg.EnableTraces,
		EnableMetrics: cfg.EnableMetrics,
		EnableLogs:    cfg.EnableLogs,
		Order:         orderService,
		Faults:        faultState,
		Metrics:       tel,
		Logger:        tel.Logger(),
	})

	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           tel.WrapHTTPHandler(handler.Router(), "http.server"),
		ReadHeaderTimeout: 5 * time.Second,
	}

	return &Application{
		cfg:       cfg,
		server:    server,
		worker:    workerRunner,
		telemetry: tel,
	}, nil
}

func (a *Application) Run(ctx context.Context) (runErr error) {
	a.telemetry.Logger().InfoContext(ctx, fmt.Sprintf("starting %s on %s", a.cfg.ServiceName, a.cfg.ListenAddr))

	workerCtx, workerCancel := context.WithCancel(ctx)
	defer workerCancel()
	go a.worker.Start(workerCtx)

	var shutdownOnce sync.Once
	var shutdownErrMu sync.Mutex
	var shutdownErr error
	recordShutdownErr := func(err error) {
		if err == nil {
			return
		}
		shutdownErrMu.Lock()
		defer shutdownErrMu.Unlock()
		shutdownErr = errors.Join(shutdownErr, err)
	}
	shutdown := func() {
		shutdownOnce.Do(func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			workerCancel()
			if err := a.server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
				recordShutdownErr(err)
			}
			if err := a.telemetry.Shutdown(shutdownCtx); err != nil {
				recordShutdownErr(err)
			}
		})
	}
	defer shutdown()

	go func() {
		<-ctx.Done()
		shutdown()
	}()

	err := a.server.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		runErr = errors.Join(runErr, err)
	}
	shutdown()
	shutdownErrMu.Lock()
	runErr = errors.Join(runErr, shutdownErr)
	shutdownErrMu.Unlock()
	return runErr
}

func LogStartupError(err error) {
	log.Printf("startup failed: %v", err)
}
