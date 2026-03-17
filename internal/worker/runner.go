package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/Xmandon/xdom/internal/faults"
	"github.com/Xmandon/xdom/internal/order"
	"github.com/Xmandon/xdom/internal/telemetry"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type Config struct {
	Interval time.Duration
	Service  *order.Service
	Logger   *slog.Logger
	Faults   *faults.State
	Metrics  *telemetry.Manager
	Tracer   oteltrace.Tracer
}

type Runner struct {
	cfg Config
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
	ctx, span := r.cfg.Tracer.Start(ctx, "worker.run_once")
	defer span.End()
	defer func() {
		if recovered := recover(); recovered != nil {
			attrs := append([]slog.Attr{slog.Any("panic", recovered)}, telemetry.TraceLogAttrs(ctx)...)
			r.cfg.Logger.LogAttrs(ctx, slog.LevelError, "worker panic recovered", attrs...)
			r.cfg.Metrics.RecordWorkerFailed(ctx, "panic_recovered")
		}
	}()

	mode, _ := r.cfg.Faults.Get()
	if mode == faults.WorkerPanic {
		r.cfg.Metrics.RecordWorkerFailed(ctx, "panic")
		panic("worker panic injected")
	}

	if err := r.cfg.Service.CancelExpiredOrders(ctx); err != nil {
		attrs := append([]slog.Attr{slog.String("error", err.Error())}, telemetry.TraceLogAttrs(ctx)...)
		r.cfg.Logger.LogAttrs(ctx, slog.LevelError, "worker failed", attrs...)
		r.cfg.Metrics.RecordWorkerFailed(ctx, "cancel_expired")
		span.RecordError(err)
		return
	}
	r.cfg.Metrics.RecordWorkerProcessed(ctx, "tick")
}
