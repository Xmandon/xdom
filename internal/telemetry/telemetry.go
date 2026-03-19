package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	logsdk "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type Config struct {
	ServiceName       string
	Environment       string
	Version           string
	CommitSHA         string
	BuildID           string
	Token             string
	OTLPEndpoint      string
	OTLPInsecure      bool
	EnableTraces      bool
	EnableMetrics     bool
	EnableLogs        bool
	ExportInterval    time.Duration
	ExportTimeout     time.Duration
	TraceBatchTimeout time.Duration
	LogBatchTimeout   time.Duration
	NetHostIP         string
}

type Manager struct {
	logger             *slog.Logger
	tracer             oteltrace.Tracer
	meter              otelmetric.Meter
	flushes            []func(context.Context) error
	shutdowns          []func(context.Context) error
	mu                 sync.RWMutex
	activePendingFn    func(context.Context) int64
	lastHeartbeatUnix  int64
	httpRequests       otelmetric.Int64Counter
	httpErrors         otelmetric.Int64Counter
	httpLatency        otelmetric.Float64Histogram
	ordersCreated      otelmetric.Int64Counter
	ordersPaid         otelmetric.Int64Counter
	ordersCancelled    otelmetric.Int64Counter
	orderCreateLatency otelmetric.Float64Histogram
	inventoryFailures  otelmetric.Int64Counter
	paymentFailures    otelmetric.Int64Counter
	paymentTimeouts    otelmetric.Int64Counter
	workerProcessed    otelmetric.Int64Counter
	workerFailed       otelmetric.Int64Counter
	faultActivations   otelmetric.Int64Counter
	heartbeats         otelmetric.Int64Counter
}

func NewManager(ctx context.Context, cfg Config) (*Manager, error) {
	cfg = normalizeConfig(cfg)
	res, err := newResource(ctx, cfg)
	if err != nil {
		return nil, err
	}

	manager := &Manager{
		logger: slog.New(slog.NewJSONHandler(os.Stdout, nil)),
		tracer: otel.Tracer(cfg.ServiceName),
		meter:  otel.Meter(cfg.ServiceName),
	}

	manager.logSignalStatus(cfg)

	if cfg.EnableTraces && cfg.OTLPEndpoint != "" && cfg.Token != "" {
		traceOptions := []otlptracehttp.Option{
			otlptracehttp.WithEndpoint(cfg.OTLPEndpoint),
			otlptracehttp.WithHeaders(map[string]string{"x-bk-token": cfg.Token}),
		}
		if cfg.OTLPInsecure {
			traceOptions = append(traceOptions, otlptracehttp.WithInsecure())
		}
		traceExporter, err := otlptracehttp.New(ctx, traceOptions...)
		if err != nil {
			_ = manager.Shutdown(ctx)
			return nil, fmt.Errorf("create trace exporter: %w", err)
		}
		traceProvider := tracesdk.NewTracerProvider(
			tracesdk.WithBatcher(
				traceExporter,
				tracesdk.WithBatchTimeout(cfg.TraceBatchTimeout),
				tracesdk.WithExportTimeout(cfg.ExportTimeout),
			),
			tracesdk.WithResource(res),
		)
		otel.SetTracerProvider(traceProvider)
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
		manager.tracer = traceProvider.Tracer(cfg.ServiceName)
		manager.flushes = append(manager.flushes, traceProvider.ForceFlush)
		manager.shutdowns = append(manager.shutdowns, traceProvider.Shutdown)
	}

	if cfg.EnableMetrics && cfg.OTLPEndpoint != "" && cfg.Token != "" {
		metricOptions := []otlpmetrichttp.Option{
			otlpmetrichttp.WithEndpoint(cfg.OTLPEndpoint),
			otlpmetrichttp.WithHeaders(map[string]string{"x-bk-token": cfg.Token}),
		}
		if cfg.OTLPInsecure {
			metricOptions = append(metricOptions, otlpmetrichttp.WithInsecure())
		}
		metricExporter, err := otlpmetrichttp.New(ctx, metricOptions...)
		if err != nil {
			_ = manager.Shutdown(ctx)
			return nil, fmt.Errorf("create metric exporter: %w", err)
		}
		meterProvider := sdkmetric.NewMeterProvider(
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(
				metricExporter,
				sdkmetric.WithInterval(cfg.ExportInterval),
				sdkmetric.WithTimeout(cfg.ExportTimeout),
			)),
			sdkmetric.WithResource(res),
		)
		otel.SetMeterProvider(meterProvider)
		manager.meter = meterProvider.Meter(cfg.ServiceName)
		manager.flushes = append(manager.flushes, meterProvider.ForceFlush)
		manager.shutdowns = append(manager.shutdowns, meterProvider.Shutdown)
	}

	if cfg.EnableLogs && cfg.OTLPEndpoint != "" && cfg.Token != "" {
		logOptions := []otlploghttp.Option{
			otlploghttp.WithEndpoint(cfg.OTLPEndpoint),
			otlploghttp.WithHeaders(map[string]string{"x-bk-token": cfg.Token}),
		}
		if cfg.OTLPInsecure {
			logOptions = append(logOptions, otlploghttp.WithInsecure())
		}
		logExporter, err := otlploghttp.New(ctx, logOptions...)
		if err != nil {
			_ = manager.Shutdown(ctx)
			return nil, fmt.Errorf("create log exporter: %w", err)
		}
		loggerProvider := logsdk.NewLoggerProvider(
			logsdk.WithProcessor(logsdk.NewBatchProcessor(
				logExporter,
				logsdk.WithExportInterval(cfg.LogBatchTimeout),
				logsdk.WithExportTimeout(cfg.ExportTimeout),
			)),
			logsdk.WithResource(res),
		)
		manager.logger = otelslog.NewLogger(cfg.ServiceName, otelslog.WithLoggerProvider(loggerProvider))
		manager.flushes = append(manager.flushes, loggerProvider.ForceFlush)
		manager.shutdowns = append(manager.shutdowns, loggerProvider.Shutdown)
	}

	if err := manager.initInstruments(); err != nil {
		_ = manager.Shutdown(ctx)
		return nil, err
	}
	return manager, nil
}

func normalizeConfig(cfg Config) Config {
	if cfg.ExportInterval <= 0 {
		cfg.ExportInterval = 5 * time.Second
	}
	if cfg.ExportTimeout <= 0 {
		cfg.ExportTimeout = 5 * time.Second
	}
	if cfg.TraceBatchTimeout <= 0 {
		cfg.TraceBatchTimeout = 2 * time.Second
	}
	if cfg.LogBatchTimeout <= 0 {
		cfg.LogBatchTimeout = 2 * time.Second
	}
	return cfg
}

func newResource(ctx context.Context, cfg Config) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{
		semconv.ServiceNameKey.String(cfg.ServiceName),
		semconv.ServiceVersionKey.String(cfg.Version),
		attribute.String("deployment.environment", cfg.Environment),
		attribute.String("commit.sha", cfg.CommitSHA),
		attribute.String("build.id", cfg.BuildID),
	}
	if cfg.NetHostIP != "" {
		attrs = append(attrs, attribute.String("net.host.ip", cfg.NetHostIP))
	}

	extraRes, err := resource.New(
		ctx,
		resource.WithHost(),
		resource.WithOS(),
		resource.WithProcess(),
		resource.WithAttributes(attrs...),
	)
	if err != nil {
		return nil, err
	}
	return resource.Merge(resource.Default(), extraRes)
}

func (m *Manager) initInstruments() error {
	var err error
	m.httpRequests, err = m.meter.Int64Counter("http_requests_total", otelmetric.WithDescription("Total number of HTTP requests"))
	if err != nil {
		return err
	}
	m.httpErrors, err = m.meter.Int64Counter("http_errors_total", otelmetric.WithDescription("Total number of HTTP errors"))
	if err != nil {
		return err
	}
	m.httpLatency, err = m.meter.Float64Histogram("http_request_duration_seconds", otelmetric.WithDescription("HTTP request duration in seconds"))
	if err != nil {
		return err
	}
	m.ordersCreated, err = m.meter.Int64Counter("orders_created_total", otelmetric.WithDescription("Number of orders created"))
	if err != nil {
		return err
	}
	m.ordersPaid, err = m.meter.Int64Counter("orders_paid_total", otelmetric.WithDescription("Number of orders paid"))
	if err != nil {
		return err
	}
	m.ordersCancelled, err = m.meter.Int64Counter("orders_cancelled_total", otelmetric.WithDescription("Number of orders cancelled"))
	if err != nil {
		return err
	}
	m.orderCreateLatency, err = m.meter.Float64Histogram("order_create_duration_seconds", otelmetric.WithDescription("Order create duration in seconds"))
	if err != nil {
		return err
	}
	m.inventoryFailures, err = m.meter.Int64Counter("inventory_reserve_failed_total", otelmetric.WithDescription("Failed inventory reservations"))
	if err != nil {
		return err
	}
	m.paymentFailures, err = m.meter.Int64Counter("payment_charge_failed_total", otelmetric.WithDescription("Failed payment charges"))
	if err != nil {
		return err
	}
	m.paymentTimeouts, err = m.meter.Int64Counter("payment_timeout_total", otelmetric.WithDescription("Payment timeouts"))
	if err != nil {
		return err
	}
	m.workerProcessed, err = m.meter.Int64Counter("worker_jobs_processed_total", otelmetric.WithDescription("Processed worker jobs"))
	if err != nil {
		return err
	}
	m.workerFailed, err = m.meter.Int64Counter("worker_jobs_failed_total", otelmetric.WithDescription("Failed worker jobs"))
	if err != nil {
		return err
	}
	m.faultActivations, err = m.meter.Int64Counter("fault_activations_total", otelmetric.WithDescription("Fault activations"))
	if err != nil {
		return err
	}
	m.heartbeats, err = m.meter.Int64Counter("service_heartbeat_total", otelmetric.WithDescription("Heartbeat signals emitted by the service"))
	if err != nil {
		return err
	}

	activePending, err := m.meter.Int64ObservableGauge("active_pending_orders", otelmetric.WithDescription("Active pending orders"))
	if err != nil {
		return err
	}
	lastHeartbeat, err := m.meter.Int64ObservableGauge("service_last_heartbeat_unixtime", otelmetric.WithDescription("Unix timestamp of the latest emitted heartbeat"))
	if err != nil {
		return err
	}
	_, err = m.meter.RegisterCallback(func(ctx context.Context, observer otelmetric.Observer) error {
		if m.activePendingFn == nil {
			goto observeHeartbeat
		}
		observer.ObserveInt64(activePending, m.activePendingFn(ctx))
	observeHeartbeat:
		m.mu.RLock()
		lastHeartbeatUnix := m.lastHeartbeatUnix
		m.mu.RUnlock()
		if lastHeartbeatUnix > 0 {
			observer.ObserveInt64(lastHeartbeat, lastHeartbeatUnix)
		}
		return nil
	}, activePending, lastHeartbeat)
	return err
}

func (m *Manager) Shutdown(ctx context.Context) error {
	var firstErr error
	for _, flush := range m.flushes {
		if err := flush(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	for i := len(m.shutdowns) - 1; i >= 0; i-- {
		shutdown := m.shutdowns[i]
		if err := shutdown(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (m *Manager) Logger() *slog.Logger {
	return m.logger
}

func (m *Manager) Tracer() oteltrace.Tracer {
	return m.tracer
}

func (m *Manager) Meter() otelmetric.Meter {
	return m.meter
}

func (m *Manager) WrapHTTPHandler(next http.Handler, spanName string) http.Handler {
	return otelhttp.NewHandler(next, spanName)
}

func (m *Manager) SetActivePendingOrdersProvider(fn func(context.Context) int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.activePendingFn = fn
}

func (m *Manager) ObserveHTTPRequest(ctx context.Context, api string, status int, latency time.Duration) {
	attrs := otelmetric.WithAttributes(attribute.String("api", api))
	m.httpRequests.Add(ctx, 1, attrs)
	m.httpLatency.Record(ctx, latency.Seconds(), attrs)
	if status >= http.StatusInternalServerError {
		m.httpErrors.Add(ctx, 1, attrs)
	}
}

func (m *Manager) RecordOrderCreated(ctx context.Context, paymentChannel string, duration time.Duration) {
	opts := otelmetric.WithAttributes(attribute.String("payment_channel", paymentChannel))
	m.ordersCreated.Add(ctx, 1, opts)
	m.orderCreateLatency.Record(ctx, duration.Seconds(), opts)
}

func (m *Manager) RecordOrderPaid(ctx context.Context, paymentChannel string) {
	m.ordersPaid.Add(ctx, 1, otelmetric.WithAttributes(attribute.String("payment_channel", paymentChannel)))
}

func (m *Manager) RecordOrderCancelled(ctx context.Context, reason string) {
	m.ordersCancelled.Add(ctx, 1, otelmetric.WithAttributes(attribute.String("result", reason)))
}

func (m *Manager) RecordInventoryFailure(ctx context.Context, reason string) {
	m.inventoryFailures.Add(ctx, 1, otelmetric.WithAttributes(attribute.String("result", reason)))
}

func (m *Manager) RecordPaymentFailure(ctx context.Context, reason string) {
	m.paymentFailures.Add(ctx, 1, otelmetric.WithAttributes(attribute.String("result", reason)))
}

func (m *Manager) RecordPaymentTimeout(ctx context.Context) {
	m.paymentTimeouts.Add(ctx, 1)
}

func (m *Manager) RecordWorkerProcessed(ctx context.Context, result string) {
	m.workerProcessed.Add(ctx, 1, otelmetric.WithAttributes(attribute.String("result", result)))
}

func (m *Manager) RecordWorkerFailed(ctx context.Context, reason string) {
	m.workerFailed.Add(ctx, 1, otelmetric.WithAttributes(attribute.String("result", reason)))
}

func (m *Manager) RecordFaultActivation(ctx context.Context, mode string) {
	m.faultActivations.Add(ctx, 1, otelmetric.WithAttributes(attribute.String("fault_mode", mode)))
}

func (m *Manager) RecordHeartbeat(ctx context.Context, source string, at time.Time) {
	m.mu.Lock()
	m.lastHeartbeatUnix = at.Unix()
	m.mu.Unlock()
	m.heartbeats.Add(ctx, 1, otelmetric.WithAttributes(attribute.String("source", source)))
}

func TraceLogAttrs(ctx context.Context) []slog.Attr {
	spanCtx := oteltrace.SpanFromContext(ctx).SpanContext()
	if !spanCtx.IsValid() {
		return nil
	}
	return []slog.Attr{
		slog.String("trace_id", spanCtx.TraceID().String()),
		slog.String("span_id", spanCtx.SpanID().String()),
	}
}

func (m *Manager) logSignalStatus(cfg Config) {
	for _, signal := range []struct {
		name    string
		enabled bool
	}{
		{name: "traces", enabled: cfg.EnableTraces},
		{name: "metrics", enabled: cfg.EnableMetrics},
		{name: "logs", enabled: cfg.EnableLogs},
	} {
		switch {
		case !signal.enabled:
			m.logger.Info("remote telemetry disabled", slog.String("signal", signal.name), slog.String("reason", "signal_disabled"))
		case cfg.OTLPEndpoint == "":
			m.logger.Warn("remote telemetry disabled", slog.String("signal", signal.name), slog.String("reason", "missing_otlp_endpoint"))
		case cfg.Token == "":
			m.logger.Warn("remote telemetry disabled", slog.String("signal", signal.name), slog.String("reason", "missing_token"))
		default:
			m.logger.Info("remote telemetry enabled",
				slog.String("signal", signal.name),
				slog.String("otlp_endpoint", cfg.OTLPEndpoint),
				slog.Bool("insecure", cfg.OTLPInsecure),
			)
		}
	}
}
