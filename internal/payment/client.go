package payment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Xmandon/xdom/internal/paymentapi"
	"github.com/Xmandon/xdom/internal/telemetry"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

var (
	ErrTimeout = errors.New("payment timeout")
	ErrCharge  = errors.New("payment charge failed")
)

const validationPanicAmount = 999.91

const validationPanicEnvKey = "RCA_VALIDATION_PANIC_ENABLED"

const directLineBugAmount = 999.92

const DirectLineBugHeader = "X-RCA-Line-Bug"

type directLineBugContextKey struct{}

type Config struct {
	BaseURL        string
	RequestTimeout int
	Logger         *slog.Logger
	Tracer         oteltrace.Tracer
}

type Client struct {
	cfg        Config
	httpClient *http.Client
}

func NewClient(cfg Config) *Client {
	return &Client{
		cfg: cfg,
		httpClient: &http.Client{
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		},
	}
}

func (c *Client) Charge(ctx context.Context, orderID string, amount float64, channel string) error {
	ctx, span := c.cfg.Tracer.Start(ctx, "payment.charge", oteltrace.WithSpanKind(oteltrace.SpanKindClient))
	defer span.End()
	span.SetAttributes(
		attribute.String("order.id", orderID),
		attribute.String("payment.channel", channel),
		attribute.Float64("payment.amount", amount),
		attribute.String("server.address", c.cfg.BaseURL),
	)

	if shouldTriggerValidationPanic(amount, channel) {
		err := errors.New("payment_charge_failed validation bug")
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		span.SetAttributes(
			attribute.String("error.code", paymentapi.ErrorCodeChargeFailed),
			attribute.String("validation.mode", "controlled_panic"),
		)
		span.AddEvent("payment.validation_panic", oteltrace.WithAttributes(
			attribute.String("error.code", paymentapi.ErrorCodeChargeFailed),
			attribute.Float64("payment.amount", amount),
		))
		attrs := append([]slog.Attr{
			slog.String("order_id", orderID),
			slog.String("payment_channel", channel),
			slog.String("error_code", paymentapi.ErrorCodeChargeFailed),
			slog.String("code_location", "internal/payment/client.go:Charge"),
			slog.String("error", err.Error()),
			slog.String("validation_mode", "controlled_panic"),
			slog.Float64("validation_amount", amount),
		}, telemetry.TraceLogAttrs(ctx)...)
		c.cfg.Logger.LogAttrs(ctx, slog.LevelError, "payment charge failed", attrs...)
		panic(err)
	}

	if err := c.triggerDirectLineBug(ctx, span, orderID, amount, channel); err != nil {
		return err
	}

	reqBody, err := json.Marshal(paymentapi.ChargeRequest{
		OrderID: orderID,
		Amount:  amount,
		Channel: channel,
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	requestCtx := ctx
	if c.cfg.RequestTimeout > 0 {
		timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(c.cfg.RequestTimeout)*time.Millisecond)
		defer cancel()
		requestCtx = timeoutCtx
	}
	endpoint := strings.TrimRight(c.cfg.BaseURL, "/") + "/charge"
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, ErrTimeout.Error())
		span.SetAttributes(attribute.String("error.code", paymentapi.ErrorCodeTimeout))
		span.AddEvent("payment.remote_error", oteltrace.WithAttributes(attribute.String("error.code", paymentapi.ErrorCodeTimeout)))
		attrs := append([]slog.Attr{
			slog.String("order_id", orderID),
			slog.String("payment_channel", channel),
			slog.String("error_code", paymentapi.ErrorCodeTimeout),
			slog.String("code_location", "internal/payment/client.go:Charge"),
			slog.String("error", err.Error()),
		}, telemetry.TraceLogAttrs(ctx)...)
		c.cfg.Logger.LogAttrs(ctx, slog.LevelWarn, "payment request failed", attrs...)
		return ErrTimeout
	}
	defer resp.Body.Close()

	var payload paymentapi.ChargeResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	span.SetAttributes(
		attribute.Int("http.response.status_code", resp.StatusCode),
		attribute.String("payment.response.status", payload.Status),
	)

	switch {
	case resp.StatusCode == http.StatusGatewayTimeout || payload.ErrorCode == paymentapi.ErrorCodeTimeout:
		span.RecordError(ErrTimeout)
		span.SetStatus(codes.Error, ErrTimeout.Error())
		span.SetAttributes(attribute.String("error.code", paymentapi.ErrorCodeTimeout))
		span.AddEvent("payment.remote_error", oteltrace.WithAttributes(attribute.String("error.code", paymentapi.ErrorCodeTimeout)))
		attrs := append([]slog.Attr{
			slog.String("order_id", orderID),
			slog.String("payment_channel", channel),
			slog.String("error_code", paymentapi.ErrorCodeTimeout),
			slog.String("code_location", "internal/payment/client.go:Charge"),
			slog.String("error", ErrTimeout.Error()),
		}, telemetry.TraceLogAttrs(ctx)...)
		c.cfg.Logger.LogAttrs(ctx, slog.LevelWarn, "payment timeout", attrs...)
		return ErrTimeout
	case resp.StatusCode >= http.StatusBadRequest || payload.ErrorCode == paymentapi.ErrorCodeChargeFailed:
		span.RecordError(ErrCharge)
		span.SetStatus(codes.Error, ErrCharge.Error())
		span.SetAttributes(attribute.String("error.code", paymentapi.ErrorCodeChargeFailed))
		span.AddEvent("payment.remote_error", oteltrace.WithAttributes(attribute.String("error.code", paymentapi.ErrorCodeChargeFailed)))
		attrs := append([]slog.Attr{
			slog.String("order_id", orderID),
			slog.String("payment_channel", channel),
			slog.String("error_code", paymentapi.ErrorCodeChargeFailed),
			slog.String("code_location", "internal/payment/client.go:Charge"),
			slog.String("error", ErrCharge.Error()),
		}, telemetry.TraceLogAttrs(ctx)...)
		c.cfg.Logger.LogAttrs(ctx, slog.LevelError, "payment charge failed", attrs...)
		return ErrCharge
	}

	attrs := append([]slog.Attr{
		slog.String("order_id", orderID),
		slog.String("payment_channel", channel),
		slog.String("authorization_id", payload.AuthorizationID),
		slog.String("code_location", "internal/payment/client.go:Charge"),
	}, telemetry.TraceLogAttrs(ctx)...)
	c.cfg.Logger.LogAttrs(ctx, slog.LevelInfo, "payment charged", attrs...)
	return nil
}

func (c *Client) triggerDirectLineBug(ctx context.Context, span oteltrace.Span, orderID string, amount float64, channel string) error {
	err := errors.New("payment_charge_failed direct line bug")
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
	span.SetAttributes(
		attribute.String("error.code", paymentapi.ErrorCodeChargeFailed),
		attribute.String("validation.mode", "direct_line_bug"),
	)
	attrs := append([]slog.Attr{
		slog.String("order_id", orderID),
		slog.String("payment_channel", channel),
		slog.String("error_code", paymentapi.ErrorCodeChargeFailed),
		slog.String("code_location", "internal/payment/client.go:triggerDirectLineBug"),
		slog.String("error", err.Error()),
		slog.String("validation_mode", "direct_line_bug"),
		slog.Float64("validation_amount", amount),
	}, telemetry.TraceLogAttrs(ctx)...)
	c.cfg.Logger.LogAttrs(ctx, slog.LevelError, "payment charge failed", attrs...)

	var bug *paymentapi.ChargeResponse
	_ = bug.Status
	return ErrCharge
}

func shouldTriggerValidationPanic(amount float64, channel string) bool {
	return strings.EqualFold(os.Getenv(validationPanicEnvKey), "true") &&
		channel == "mockpay" &&
		amount >= validationPanicAmount &&
		amount < validationPanicAmount+0.01
}

func WithDirectLineBug(ctx context.Context, enabled bool) context.Context {
	return context.WithValue(ctx, directLineBugContextKey{}, enabled)
}

func shouldTriggerDirectLineBug(ctx context.Context, amount float64, channel string) bool {
	enabled, _ := ctx.Value(directLineBugContextKey{}).(bool)
	return enabled &&
		channel == "mockpay" &&
		amount >= directLineBugAmount &&
		amount < directLineBugAmount+0.01
}
