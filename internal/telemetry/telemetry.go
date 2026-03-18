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
	ServiceName   string
	Environment   string
	Version       string
	CommitSHA     string
	BuildID       string
	Token         string
	OTLPEndpoint  string
	EnableTraces  bool
	EnableMetrics bool
	EnableLogs    bool
	NetHostIP     string
}

type Manager struct {
	logger             *slog.Logger
	tracer             oteltrace.Tracer
	meter              otelmetric.Meter
	shutdowns          []func(context.Context) error
	mu                 sync.RWMutex
	activePendingFn    func(context.Context) int64
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
}

func NewManager(ctx context.Context, cfg Config) (*Manager, error) {
	res, err := newResource(ctx, cfg)
	if err != nil {
		return nil, err
	}

	manager := &Manager{
		logger: slog.New(slog.NewJSONHandler(os.Stdout, nil)),
		tracer: otel.Tracer(cfg.ServiceName),
		meter:  otel.Meter(cfg.ServiceName),
	}

	if cfg.EnableTraces && cfg.OTLPEndpoint != "" && cfg.Token != "" {
		traceExporter, err := otlptracehttp.New(
			ctx,
			otlptracehttp.WithEndpoint(cfg.OTLPEndpoint),
			otlptracehttp.WithInsecure(),
			otlptracehttp.WithHeaders(map[string]string{"x-bk-token": cfg.Token}),
		)
		if err != nil {
			return nil, fmt.Errorf("create trace exporter: %w", err)
		}
		traceProvider := tracesdk.NewTracerProvider(
			tracesdk.WithBatcher(traceExporter),
			tracesdk.WithResource(res),
		)
		otel.SetTracerProvider(traceProvider)
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
		manager.tracer = traceProvider.Tracer(cfg.ServiceName)
		manager.shutdowns = append(manager.shutdowns, traceProvider.Shutdown)
	}

	if cfg.EnableMetrics && cfg.OTLPEndpoint != "" && cfg.Token != "" {
		metricExporter, err := otlpmetrichttp.New(
			ctx,
			otlpmetrichttp.WithEndpoint(cfg.OTLPEndpoint),
			otlpmetrichttp.WithInsecure(),
			otlpmetrichttp.WithHeaders(map[string]string{"x-bk-token": cfg.Token}),
		)
		if err != nil {
			return nil, fmt.Errorf("create metric exporter: %w", err)
		}
		meterProvider := sdkmetric.NewMeterProvider(
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter, sdkmetric.WithInterval(15*time.Second))),
			sdkmetric.WithResource(res),
		)
		otel.SetMeterProvider(meterProvider)
		manager.meter = meterProvider.Meter(cfg.ServiceName)
		manager.shutdowns = append(manager.shutdowns, meterProvider.Shutdown)
	}

	if cfg.EnableLogs && cfg.OTLPEndpoint != "" && cfg.Token != "" {
		logExporter, err := otlploghttp.New(
			ctx,
			otlploghttp.WithEndpoint(cfg.OTLPEndpoint),
			otlploghttp.WithInsecure(),
			otlploghttp.WithHeaders(map[string]string{"x-bk-token": cfg.Token}),
		)
		if err != nil {
			return nil, fmt.Errorf("create log exporter: %w", err)
		}
		loggerProvider := logsdk.NewLoggerProvider(
			logsdk.WithProcessor(logsdk.NewBatchProcessor(logExporter)),
			logsdk.WithResource(res),
		)
		manager.logger = otelslog.NewLogger(cfg.ServiceName, otelslog.WithLoggerProvider(loggerProvider))
		manager.shutdowns = append(manager.shutdowns, loggerProvider.Shutdown)
	}

	if err := manager.initInstruments(); err != nil {
		return nil, err
	}
	return manager, nil
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

	activePending, err := m.meter.Int64ObservableGauge("active_pending_orders", otelmetric.WithDescription("Active pending orders"))
	if err != nil {
		return err
	}
	_, err = m.meter.RegisterCallback(func(ctx context.Context, observer otelmetric.Observer) error {
		if m.activePendingFn == nil {
			return nil
		}
		observer.ObserveInt64(activePending, m.activePendingFn(ctx))
		return nil
	}, activePending)
	return err
}

func (m *Manager) Shutdown(ctx context.Context) error {
	var firstErr error
	for _, shutdown := range m.shutdowns {
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
