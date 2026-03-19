package xpay

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/Xmandon/xdom/internal/faults"
	"github.com/Xmandon/xdom/internal/telemetry"
)

type Application struct {
	cfg       Config
	server    *http.Server
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
	handler, err := NewHandler(HandlerConfig{
		ServiceName:   cfg.ServiceName,
		Environment:   cfg.Environment,
		Version:       cfg.Version,
		CommitSHA:     cfg.CommitSHA,
		BuildID:       cfg.BuildID,
		AdminToken:    cfg.AdminToken,
		BaseLatencyMS: cfg.BaseLatencyMS,
		EnableTraces:  cfg.EnableTraces,
		EnableMetrics: cfg.EnableMetrics,
		EnableLogs:    cfg.EnableLogs,
		Faults:        faultState,
		Metrics:       tel,
		Logger:        tel.Logger(),
	})
	if err != nil {
		_ = tel.Shutdown(ctx)
		return nil, fmt.Errorf("init handler: %w", err)
	}

	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           tel.WrapHTTPHandler(handler.Router(), "http.server"),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return &Application{
		cfg:       cfg,
		server:    server,
		telemetry: tel,
	}, nil
}

func (a *Application) Run(ctx context.Context) (runErr error) {
	a.telemetry.Logger().InfoContext(ctx, fmt.Sprintf("starting %s on %s", a.cfg.ServiceName, a.cfg.ListenAddr))

	var shutdownOnce sync.Once
	var shutdownErr error
	var shutdownErrMu sync.Mutex
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
