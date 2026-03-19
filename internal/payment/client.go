package payment

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/Xmandon/xdom/internal/faults"
	"github.com/Xmandon/xdom/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

var (
	ErrTimeout = errors.New("payment timeout")
	ErrCharge  = errors.New("payment charge failed")
)

type Config struct {
	BaseLatencyMS int
	Logger        *slog.Logger
	Faults        *faults.State
	Tracer        oteltrace.Tracer
	Metrics       *telemetry.Manager
}

type Client struct {
	cfg Config
}

func NewClient(cfg Config) *Client {
	return &Client{cfg: cfg}
}

func (c *Client) Charge(ctx context.Context, orderID string, amount float64, channel string) error {
	ctx, span := c.cfg.Tracer.Start(ctx, "payment.charge", oteltrace.WithSpanKind(oteltrace.SpanKindClient))
	defer span.End()
	span.SetAttributes(
		attribute.String("order.id", orderID),
		attribute.String("payment.channel", channel),
		attribute.Float64("payment.amount", amount),
	)

	mode, delayMS := c.cfg.Faults.Get()
	sleepMS := c.cfg.BaseLatencyMS
	if mode == faults.PaymentTimeout && delayMS > sleepMS {
		sleepMS = delayMS
	}
	time.Sleep(time.Duration(sleepMS) * time.Millisecond)

	switch mode {
	case faults.PaymentTimeout:
		span.RecordError(ErrTimeout)
		span.SetAttributes(attribute.String("fault.mode", string(mode)))
		span.SetStatus(codes.Error, ErrTimeout.Error())
		span.AddEvent("fault.injected", oteltrace.WithAttributes(attribute.String("fault.mode", string(mode))))
		attrs := append([]slog.Attr{
			slog.String("order_id", orderID),
			slog.String("payment_channel", channel),
			slog.String("fault_mode", string(mode)),
			slog.String("error_code", "payment_timeout"),
			slog.String("code_location", "internal/payment/client.go:Charge"),
			slog.String("error", ErrTimeout.Error()),
		}, telemetry.TraceLogAttrs(ctx)...)
		c.cfg.Logger.LogAttrs(ctx, slog.LevelWarn, "payment timeout injected", attrs...)
		return ErrTimeout
	case faults.PaymentError:
		span.RecordError(ErrCharge)
		span.SetAttributes(attribute.String("fault.mode", string(mode)))
		span.SetStatus(codes.Error, ErrCharge.Error())
		span.AddEvent("fault.injected", oteltrace.WithAttributes(attribute.String("fault.mode", string(mode))))
		attrs := append([]slog.Attr{
			slog.String("order_id", orderID),
			slog.String("payment_channel", channel),
			slog.String("fault_mode", string(mode)),
			slog.String("error_code", "payment_charge_failed"),
			slog.String("code_location", "internal/payment/client.go:Charge"),
			slog.String("error", ErrCharge.Error()),
		}, telemetry.TraceLogAttrs(ctx)...)
		c.cfg.Logger.LogAttrs(ctx, slog.LevelError, "payment charge failed", attrs...)
		return ErrCharge
	default:
		attrs := append([]slog.Attr{
			slog.String("order_id", orderID),
			slog.String("payment_channel", channel),
			slog.String("code_location", "internal/payment/client.go:Charge"),
		}, telemetry.TraceLogAttrs(ctx)...)
		c.cfg.Logger.LogAttrs(ctx, slog.LevelInfo, "payment charged", attrs...)
		return nil
	}
}
