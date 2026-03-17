package app

import (
	"encoding/json"
	"log"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

type Server struct {
	cfg     Config
	faults  *FaultState
	metrics *Metrics
	server  *http.Server
}

type FaultRequest struct {
	Mode    FaultMode `json:"mode"`
	DelayMS int       `json:"delay_ms"`
}

func NewServer(cfg Config) *Server {
	s := &Server{
		cfg:     cfg,
		faults:  NewFaultState(),
		metrics: NewMetrics(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.wrap(s.handleHealthz))
	mux.HandleFunc("/api/demo", s.wrap(s.handleDemo))
	mux.HandleFunc("/admin/fault", s.wrap(s.handleFault))
	mux.HandleFunc("/metrics", s.wrap(s.handleMetrics))
	mux.HandleFunc("/version", s.wrap(s.handleVersion))

	s.server = &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	return s
}

func (s *Server) ListenAndServe() error {
	return s.server.ListenAndServe()
}

func (s *Server) wrap(next func(http.ResponseWriter, *http.Request) (int, any)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		status := http.StatusOK

		defer func() {
			if recovered := recover(); recovered != nil {
				status = http.StatusInternalServerError
				s.metrics.RecordPanic()
				s.metrics.Observe(status, time.Since(started))
				http.Error(w, "panic recovered", status)
				log.Printf("panic recovered: %v", recovered)
				log.Printf("%s", debug.Stack())
			}
		}()

		status, payload := next(w, r)
		if payload != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(payload)
		}

		s.metrics.Observe(status, time.Since(started))
	}
}

func (s *Server) handleHealthz(_ http.ResponseWriter, _ *http.Request) (int, any) {
	mode, _ := s.faults.Get()
	if mode == FaultHealthFail {
		return http.StatusServiceUnavailable, map[string]any{"status": "degraded", "fault_mode": mode}
	}
	return http.StatusOK, map[string]any{"status": "ok", "service": s.cfg.ServiceName, "env": s.cfg.Environment}
}

func (s *Server) handleDemo(_ http.ResponseWriter, _ *http.Request) (int, any) {
	mode, delayMS := s.faults.Get()
	switch mode {
	case FaultTimeout:
		time.Sleep(time.Duration(delayMS) * time.Millisecond)
		return http.StatusGatewayTimeout, map[string]any{"error": "timeout", "delay_ms": delayMS}
	case FaultError500:
		return http.StatusInternalServerError, map[string]any{"error": "forced 500"}
	case FaultPanic:
		panic("forced panic for drill")
	default:
		return http.StatusOK, map[string]any{
			"message":    "hello from xdom",
			"service":    s.cfg.ServiceName,
			"env":        s.cfg.Environment,
			"version":    s.cfg.Version,
			"commit_sha": s.cfg.CommitSHA,
			"build_id":   s.cfg.BuildID,
		}
	}
}

func (s *Server) handleFault(_ http.ResponseWriter, r *http.Request) (int, any) {
	if r.Method != http.MethodPost {
		return http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"}
	}
	token := strings.TrimSpace(r.Header.Get("X-Admin-Token"))
	if token == "" || token != s.cfg.AdminToken {
		return http.StatusUnauthorized, map[string]any{"error": "unauthorized"}
	}

	var req FaultRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return http.StatusBadRequest, map[string]any{"error": "invalid_json"}
	}

	switch req.Mode {
	case FaultNone, FaultTimeout, FaultError500, FaultPanic, FaultHealthFail:
	default:
		return http.StatusBadRequest, map[string]any{"error": "unsupported_fault_mode"}
	}

	if req.DelayMS <= 0 {
		req.DelayMS = 1500
	}
	s.faults.Set(req.Mode, req.DelayMS)
	return http.StatusOK, map[string]any{"status": "updated", "mode": req.Mode, "delay_ms": req.DelayMS}
}

func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request) (int, any) {
	mode, _ := s.faults.Get()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = w.Write([]byte(s.metrics.Render(s.cfg.ServiceName, s.cfg.Environment, mode)))
	return http.StatusOK, nil
}

func (s *Server) handleVersion(_ http.ResponseWriter, _ *http.Request) (int, any) {
	return http.StatusOK, map[string]any{
		"service":    s.cfg.ServiceName,
		"env":        s.cfg.Environment,
		"version":    s.cfg.Version,
		"commit_sha": s.cfg.CommitSHA,
		"build_id":   s.cfg.BuildID,
	}
}
