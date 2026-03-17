package app

import (
	"fmt"
	"sync"
	"time"
)

type Metrics struct {
	mu            sync.Mutex
	requestsTotal int64
	errorsTotal   int64
	panicTotal    int64
	lastLatencyMS int64
}

func NewMetrics() *Metrics {
	return &Metrics{}
}

func (m *Metrics) Observe(statusCode int, latency time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requestsTotal++
	if statusCode >= 500 {
		m.errorsTotal++
	}
	m.lastLatencyMS = latency.Milliseconds()
}

func (m *Metrics) RecordPanic() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.panicTotal++
}

func (m *Metrics) Render(service string, env string, mode FaultMode) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return fmt.Sprintf(
		"# TYPE xdom_requests_total counter\n"+
			"xdom_requests_total{service=%q,env=%q} %d\n"+
			"# TYPE xdom_errors_total counter\n"+
			"xdom_errors_total{service=%q,env=%q} %d\n"+
			"# TYPE xdom_panics_total counter\n"+
			"xdom_panics_total{service=%q,env=%q} %d\n"+
			"# TYPE xdom_last_latency_ms gauge\n"+
			"xdom_last_latency_ms{service=%q,env=%q} %d\n"+
			"# TYPE xdom_fault_mode_info gauge\n"+
			"xdom_fault_mode_info{service=%q,env=%q,mode=%q} 1\n",
		service, env, m.requestsTotal,
		service, env, m.errorsTotal,
		service, env, m.panicTotal,
		service, env, m.lastLatencyMS,
		service, env, mode,
	)
}
