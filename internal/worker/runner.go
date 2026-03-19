package worker

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/Xmandon/xdom/internal/faults"
	"github.com/Xmandon/xdom/internal/order"
	"github.com/Xmandon/xdom/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type Config struct {
	Interval             time.Duration
	HeartbeatLogInterval time.Duration
	Service              *order.Service
	Logger               *slog.Logger
	Faults               *faults.State
	Metrics              *telemetry.Manager
	Tracer               oteltrace.Tracer
}

type Runner struct {
	cfg              Config
	lastHeartbeatLog time.Time
}

func NewRunner(cfg Config) *Runner {
	return &Runner{cfg: cfg}
}

func (r *Runner) Start(ctx context.Context) {
	ticker := time.NewTicker(r.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.runOnce(ctx)
		}
	}
}

func (r *Runner) runOnce(ctx context.Context) {
	heartbeatAt := time.Now().UTC()
	ctx, span := r.cfg.Tracer.Start(ctx, "worker.run_once", oteltrace.WithSpanKind(oteltrace.SpanKindInternal))
	defer span.End()
	defer r.emitHeartbeat(ctx, heartbeatAt, span)
	defer func() {
		if recovered := recover(); recovered != nil {
			err := fmt.Errorf("worker panic injected: %v", recovered)
			span.RecordError(err)
			span.SetAttributes(attribute.String("fault.mode", string(faults.WorkerPanic)))
			span.SetStatus(codes.Error, "worker_panic")
			span.AddEvent("panic.recovered", oteltrace.WithAttributes(
				attribute.String("fault.mode", string(faults.WorkerPanic)),
				attribute.String("panic.message", fmt.Sprint(recovered)),
			))
			attrs := append([]slog.Attr{
				slog.Any("panic", recovered),
				slog.String("fault_mode", string(faults.WorkerPanic)),
				slog.String("error_code", "worker_panic"),
				slog.String("code_location", "internal/worker/runner.go:runOnce"),
				slog.String("stack_trace", string(debug.Stack())),
			}, telemetry.TraceLogAttrs(ctx)...)
			r.cfg.Logger.LogAttrs(ctx, slog.LevelError, "worker panic recovered", attrs...)
			r.cfg.Metrics.RecordWorkerFailed(ctx, "panic_recovered")
		}
	}()

	mode, _ := r.cfg.Faults.Get()
	if mode == faults.WorkerPanic {
		span.SetAttributes(attribute.String("fault.mode", string(mode)))
		span.AddEvent("fault.injected", oteltrace.WithAttributes(attribute.String("fault.mode", string(mode))))
		r.cfg.Metrics.RecordWorkerFailed(ctx, "panic")
		panic("worker panic injected")
	}

	if err := r.cfg.Service.CancelExpiredOrders(ctx); err != nil {
		attrs := append([]slog.Attr{
			slog.String("error", err.Error()),
			slog.String("error_code", "worker_job_failed"),
			slog.String("code_location", "internal/worker/runner.go:runOnce"),
		}, telemetry.TraceLogAttrs(ctx)...)
		r.cfg.Logger.LogAttrs(ctx, slog.LevelError, "worker failed", attrs...)
		r.cfg.Metrics.RecordWorkerFailed(ctx, "cancel_expired")
		span.RecordError(err)
		return
	}
	r.cfg.Metrics.RecordWorkerProcessed(ctx, "tick")
}

func (r *Runner) emitHeartbeat(ctx context.Context, at time.Time, span oteltrace.Span) {
	r.cfg.Metrics.RecordHeartbeat(ctx, "worker", at)
	span.AddEvent("heartbeat.tick", oteltrace.WithAttributes(
		attribute.String("heartbeat.source", "worker"),
		attribute.Int64("heartbeat.unix", at.Unix()),
	))

	if r.cfg.HeartbeatLogInterval <= 0 {
		return
	}
	if !r.lastHeartbeatLog.IsZero() && at.Sub(r.lastHeartbeatLog) < r.cfg.HeartbeatLogInterval {
		return
	}

	attrs := append([]slog.Attr{
		slog.String("heartbeat_source", "worker"),
		slog.String("code_location", "internal/worker/runner.go:emitHeartbeat"),
		slog.Time("heartbeat_at", at),
	}, telemetry.TraceLogAttrs(ctx)...)
	r.cfg.Logger.LogAttrs(ctx, slog.LevelInfo, "telemetry heartbeat", attrs...)
	r.lastHeartbeatLog = at
}
