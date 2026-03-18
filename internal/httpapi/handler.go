package httpapi

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Xmandon/xdom/internal/faults"
	"github.com/Xmandon/xdom/internal/order"
	"github.com/Xmandon/xdom/internal/repository"
	"github.com/Xmandon/xdom/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
)

//go:embed ui/*
var uiFiles embed.FS

type Config struct {
	ServiceName string
	Environment string
	Version     string
	CommitSHA   string
	BuildID     string
	AdminToken  string
	EnableTraces  bool
	EnableMetrics bool
	EnableLogs    bool
	Order       *order.Service
	Faults      *faults.State
	Metrics     *telemetry.Manager
	Logger      *slog.Logger
}

type Handler struct {
	cfg      Config
	mux      *http.ServeMux
	indexHTML []byte
}

type faultRequest struct {
	Mode    faults.Mode `json:"mode"`
	DelayMS int         `json:"delay_ms"`
}

func NewHandler(cfg Config) *Handler {
	uiSub, err := fs.Sub(uiFiles, "ui")
	if err != nil {
		panic(err)
	}
	indexHTML, err := fs.ReadFile(uiSub, "index.html")
	if err != nil {
		panic(err)
	}

	h := &Handler{
		cfg:       cfg,
		mux:       http.NewServeMux(),
		indexHTML: indexHTML,
	}
	h.mux.Handle("/", h.observeHandler("/", http.HandlerFunc(h.handleRoot)))
	h.mux.Handle("/ui", h.observeHandler("/ui", http.HandlerFunc(h.handleUI)))
	h.mux.Handle("/assets/", h.observeHandler("/assets", http.StripPrefix("/assets/", http.FileServer(http.FS(uiSub)))))
	h.mux.HandleFunc("/healthz", h.wrap("/healthz", h.handleHealthz))
	h.mux.HandleFunc("/api/orders", h.wrap("/api/orders", h.handleCreateOrder))
	h.mux.HandleFunc("/api/orders/", h.wrap("/api/orders/:id", h.handleOrderByID))
	h.mux.HandleFunc("/api/inventory", h.wrap("/api/inventory", h.handleInventory))
	h.mux.HandleFunc("/admin/fault", h.wrap("/admin/fault", h.handleFault))
	h.mux.HandleFunc("/metrics", h.wrap("/metrics", h.handleMetrics))
	h.mux.HandleFunc("/version", h.wrap("/version", h.handleVersion))
	return h
}

func (h *Handler) Router() http.Handler {
	return h.mux
}

func (h *Handler) observeHandler(api string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		h.cfg.Metrics.ObserveHTTPRequest(r.Context(), api, recorder.status, time.Since(started))
	})
}

func (h *Handler) wrap(api string, next func(http.ResponseWriter, *http.Request) (int, any)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		status := http.StatusOK

		defer func() {
			h.cfg.Metrics.ObserveHTTPRequest(r.Context(), api, status, time.Since(started))
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

func (h *Handler) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/ui", http.StatusFound)
}

func (h *Handler) handleUI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(h.indexHTML)
}

func (h *Handler) handleHealthz(_ http.ResponseWriter, _ *http.Request) (int, any) {
	mode, _ := h.cfg.Faults.Get()
	if mode == faults.HealthFail {
		return http.StatusServiceUnavailable, map[string]any{"status": "degraded", "fault_mode": mode}
	}
	return http.StatusOK, map[string]any{"status": "ok", "service": h.cfg.ServiceName, "env": h.cfg.Environment}
}

func (h *Handler) handleCreateOrder(_ http.ResponseWriter, r *http.Request) (int, any) {
	if r.Method != http.MethodPost {
		return http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"}
	}
	var input order.CreateOrderInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		return http.StatusBadRequest, map[string]any{"error": "invalid_json"}
	}
	if input.UserID == "" || input.SKU == "" || input.Quantity <= 0 || input.Amount <= 0 {
		return http.StatusBadRequest, map[string]any{"error": "missing_required_fields"}
	}
	if input.PaymentChannel == "" {
		input.PaymentChannel = "mockpay"
	}
	resp, err := h.cfg.Order.CreateOrder(r.Context(), input)
	if err != nil {
		return http.StatusInternalServerError, map[string]any{"error": err.Error(), "order": resp}
	}
	return http.StatusCreated, resp
}

func (h *Handler) handleOrderByID(_ http.ResponseWriter, r *http.Request) (int, any) {
	path := strings.TrimPrefix(r.URL.Path, "/api/orders/")
	path = strings.Trim(path, "/")
	if path == "" {
		return http.StatusNotFound, map[string]any{"error": "not_found"}
	}

	if strings.HasSuffix(path, "/cancel") {
		orderID := strings.TrimSuffix(path, "/cancel")
		orderID = strings.Trim(orderID, "/")
		if r.Method != http.MethodPost {
			return http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"}
		}
		resp, err := h.cfg.Order.CancelOrder(r.Context(), orderID, "manual")
		if err != nil {
			return toError(err)
		}
		return http.StatusOK, resp
	}

	if r.Method != http.MethodGet {
		return http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"}
	}
	resp, err := h.cfg.Order.GetOrder(r.Context(), path)
	if err != nil {
		return toError(err)
	}
	return http.StatusOK, resp
}

func (h *Handler) handleInventory(_ http.ResponseWriter, r *http.Request) (int, any) {
	if r.Method != http.MethodGet {
		return http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"}
	}
	items, err := h.cfg.Order.ListInventory(r.Context())
	if err != nil {
		return http.StatusInternalServerError, map[string]any{"error": err.Error()}
	}
	return http.StatusOK, map[string]any{"items": items}
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
	case faults.None, faults.PaymentTimeout, faults.PaymentError, faults.DBSlowQuery, faults.DBWriteError, faults.InventoryConflict, faults.WorkerPanic, faults.HealthFail:
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
		slog.String("code_location", "internal/httpapi/handler.go:handleFault"),
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

func toError(err error) (int, any) {
	switch {
	case errorsIs(err, repository.ErrNotFound):
		return http.StatusNotFound, map[string]any{"error": err.Error()}
	case errorsIs(err, repository.ErrInventoryConflict):
		return http.StatusConflict, map[string]any{"error": err.Error()}
	default:
		return http.StatusInternalServerError, map[string]any{"error": err.Error()}
	}
}

func errorsIs(err error, target error) bool {
	return err != nil && target != nil && (err == target || strings.Contains(err.Error(), target.Error()))
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
