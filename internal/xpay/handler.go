package xpay

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Xmandon/xdom/internal/faults"
	"github.com/Xmandon/xdom/internal/paymentapi"
	"github.com/Xmandon/xdom/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	otelmetric "go.opentelemetry.io/otel/metric"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type HandlerConfig struct {
	ServiceName   string
	Environment   string
	Version       string
	CommitSHA     string
	BuildID       string
	AdminToken    string
	BaseLatencyMS int
	EnableTraces  bool
	EnableMetrics bool
	EnableLogs    bool
	Faults        *faults.State
	Metrics       *telemetry.Manager
	Logger        *slog.Logger
}

type Handler struct {
	cfg             HandlerConfig
	mux             *http.ServeMux
	requests        otelmetric.Int64Counter
	failures        otelmetric.Int64Counter
	requestDuration otelmetric.Float64Histogram
}

type faultRequest struct {
	Mode    faults.Mode `json:"mode"`
	DelayMS int         `json:"delay_ms"`
}

func NewHandler(cfg HandlerConfig) (*Handler, error) {
	requests, err := cfg.Metrics.Meter().Int64Counter("payment_requests_total", otelmetric.WithDescription("Total number of payment requests"))
	if err != nil {
		return nil, err
	}
	failures, err := cfg.Metrics.Meter().Int64Counter("payment_failures_total", otelmetric.WithDescription("Failed payment requests"))
	if err != nil {
		return nil, err
	}
	requestDuration, err := cfg.Metrics.Meter().Float64Histogram("payment_request_duration_seconds", otelmetric.WithDescription("Payment request duration in seconds"))
	if err != nil {
		return nil, err
	}

	h := &Handler{
		cfg:             cfg,
		mux:             http.NewServeMux(),
		requests:        requests,
		failures:        failures,
		requestDuration: requestDuration,
	}
	h.mux.HandleFunc("/healthz", h.wrap("/healthz", h.handleHealthz))
	h.mux.HandleFunc("/charge", h.wrap("/charge", h.handleCharge))
	h.mux.HandleFunc("/admin/fault", h.wrap("/admin/fault", h.handleFault))
	h.mux.HandleFunc("/metrics", h.wrap("/metrics", h.handleMetrics))
	h.mux.HandleFunc("/version", h.wrap("/version", h.handleVersion))
	return h, nil
}

func (h *Handler) Router() http.Handler {
	return h.mux
}

func (h *Handler) wrap(api string, next func(http.ResponseWriter, *http.Request) (int, any)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		status := http.StatusOK
		defer func() {
			h.observeRequest(r.Context(), api, status, time.Since(started))
		}()

		status, payload := next(w, r)
		if payload == nil {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(payload)
	}
}

func (h *Handler) observeRequest(ctx context.Context, api string, status int, latency time.Duration) {
	attrs := otelmetric.WithAttributes(attribute.String("api", api))
	h.requests.Add(ctx, 1, attrs)
	h.requestDuration.Record(ctx, latency.Seconds(), attrs)
	if status >= http.StatusBadRequest {
		h.failures.Add(ctx, 1, otelmetric.WithAttributes(
			attribute.String("api", api),
			attribute.Int("status", status),
		))
	}
}

func (h *Handler) handleHealthz(_ http.ResponseWriter, _ *http.Request) (int, any) {
	mode, _ := h.cfg.Faults.Get()
	if mode == faults.HealthFail {
		return http.StatusServiceUnavailable, map[string]any{"status": "degraded", "fault_mode": mode}
	}
	return http.StatusOK, map[string]any{"status": "ok", "service": h.cfg.ServiceName, "env": h.cfg.Environment}
}

func (h *Handler) handleCharge(_ http.ResponseWriter, r *http.Request) (int, any) {
	if r.Method != http.MethodPost {
		return http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"}
	}

	var req paymentapi.ChargeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return http.StatusBadRequest, map[string]any{"error": "invalid_json"}
	}
	if req.OrderID == "" || req.Amount <= 0 || req.Channel == "" {
		return http.StatusBadRequest, map[string]any{"error": "missing_required_fields"}
	}

	span := oteltrace.SpanFromContext(r.Context())
	span.SetAttributes(
		attribute.String("order.id", req.OrderID),
		attribute.String("payment.channel", req.Channel),
		attribute.Float64("payment.amount", req.Amount),
	)

	mode, delayMS := h.cfg.Faults.Get()
	sleepMS := h.cfg.BaseLatencyMS
	if mode == faults.PaymentTimeout && delayMS > sleepMS {
		sleepMS = delayMS
	}
	time.Sleep(time.Duration(sleepMS) * time.Millisecond)

	switch mode {
	case faults.PaymentTimeout:
		err := fmt.Errorf("payment timeout")
		span.RecordError(err)
		span.SetAttributes(attribute.String("fault.mode", string(mode)))
		span.SetStatus(codes.Error, paymentapi.ErrorCodeTimeout)
		span.AddEvent("fault.injected", oteltrace.WithAttributes(attribute.String("fault.mode", string(mode))))
		attrs := append([]slog.Attr{
			slog.String("order_id", req.OrderID),
			slog.String("payment_channel", req.Channel),
			slog.String("fault_mode", string(mode)),
			slog.String("error_code", paymentapi.ErrorCodeTimeout),
			slog.String("code_location", "internal/xpay/handler.go:handleCharge"),
		}, telemetry.TraceLogAttrs(r.Context())...)
		h.cfg.Logger.LogAttrs(r.Context(), slog.LevelWarn, "payment timeout injected", attrs...)
		return http.StatusGatewayTimeout, paymentapi.ChargeResponse{
			Status:    paymentapi.StatusFailed,
			ErrorCode: paymentapi.ErrorCodeTimeout,
			Message:   err.Error(),
		}
	case faults.PaymentError:
		err := fmt.Errorf("payment charge failed")
		span.RecordError(err)
		span.SetAttributes(attribute.String("fault.mode", string(mode)))
		span.SetStatus(codes.Error, paymentapi.ErrorCodeChargeFailed)
		span.AddEvent("fault.injected", oteltrace.WithAttributes(attribute.String("fault.mode", string(mode))))
		attrs := append([]slog.Attr{
			slog.String("order_id", req.OrderID),
			slog.String("payment_channel", req.Channel),
			slog.String("fault_mode", string(mode)),
			slog.String("error_code", paymentapi.ErrorCodeChargeFailed),
			slog.String("code_location", "internal/xpay/handler.go:handleCharge"),
		}, telemetry.TraceLogAttrs(r.Context())...)
		h.cfg.Logger.LogAttrs(r.Context(), slog.LevelError, "payment charge failed", attrs...)
		return http.StatusInternalServerError, paymentapi.ChargeResponse{
			Status:    paymentapi.StatusFailed,
			ErrorCode: paymentapi.ErrorCodeChargeFailed,
			Message:   err.Error(),
		}
	default:
		authID := fmt.Sprintf("pay-%d", time.Now().UnixNano())
		attrs := append([]slog.Attr{
			slog.String("order_id", req.OrderID),
			slog.String("payment_channel", req.Channel),
			slog.String("authorization_id", authID),
			slog.String("code_location", "internal/xpay/handler.go:handleCharge"),
		}, telemetry.TraceLogAttrs(r.Context())...)
		h.cfg.Logger.LogAttrs(r.Context(), slog.LevelInfo, "payment charged", attrs...)
		return http.StatusOK, paymentapi.ChargeResponse{
			Status:          paymentapi.StatusSucceeded,
			AuthorizationID: authID,
		}
	}
}

func (h *Handler) handleFault(_ http.ResponseWriter, r *http.Request) (int, any) {
	if r.Method != http.MethodPost {
		return http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"}
	}
	token := strings.TrimSpace(r.Header.Get("X-Admin-Token"))
	if token == "" || token != h.cfg.AdminToken {
		return http.StatusUnauthorized, map[string]any{"error": "unauthorized"}
	}

	var req faultRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return http.StatusBadRequest, map[string]any{"error": "invalid_json"}
	}
	switch req.Mode {
	case faults.None, faults.PaymentTimeout, faults.PaymentError, faults.HealthFail:
	default:
		return http.StatusBadRequest, map[string]any{"error": "unsupported_fault_mode"}
	}
	if req.DelayMS <= 0 {
		req.DelayMS = 1500
	}
	h.cfg.Faults.Set(req.Mode, req.DelayMS)
	h.cfg.Metrics.RecordFaultActivation(r.Context(), string(req.Mode))
	currentSpan := oteltrace.SpanFromContext(r.Context())
	currentSpan.SetAttributes(attribute.String("fault.mode", string(req.Mode)))
	attrs := append([]slog.Attr{
		slog.String("fault_mode", string(req.Mode)),
		slog.String("error_code", "fault_mode_updated"),
		slog.String("code_location", "internal/xpay/handler.go:handleFault"),
	}, telemetry.TraceLogAttrs(r.Context())...)
	h.cfg.Logger.LogAttrs(r.Context(), slog.LevelWarn, "fault mode updated", attrs...)
	return http.StatusOK, map[string]any{"status": "updated", "mode": req.Mode, "delay_ms": req.DelayMS}
}

func (h *Handler) handleMetrics(_ http.ResponseWriter, _ *http.Request) (int, any) {
	mode, delayMS := h.cfg.Faults.Get()
	return http.StatusOK, map[string]any{
		"service": h.cfg.ServiceName,
		"env":     h.cfg.Environment,
		"fault": map[string]any{
			"mode":     mode,
			"delay_ms": delayMS,
		},
		"otlp_enabled": map[string]bool{
			"traces":  h.cfg.EnableTraces,
			"metrics": h.cfg.EnableMetrics,
			"logs":    h.cfg.EnableLogs,
		},
	}
}

func (h *Handler) handleVersion(_ http.ResponseWriter, _ *http.Request) (int, any) {
	return http.StatusOK, map[string]any{
		"service":    h.cfg.ServiceName,
		"env":        h.cfg.Environment,
		"version":    h.cfg.Version,
		"commit_sha": h.cfg.CommitSHA,
		"build_id":   h.cfg.BuildID,
	}
}
