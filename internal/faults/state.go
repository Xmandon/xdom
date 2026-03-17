package faults

import "sync"

type Mode string

const (
	None              Mode = "none"
	PaymentTimeout    Mode = "payment_timeout"
	PaymentError      Mode = "payment_error"
	DBSlowQuery       Mode = "db_slow_query"
	DBWriteError      Mode = "db_write_error"
	InventoryConflict Mode = "inventory_conflict"
	WorkerPanic       Mode = "worker_panic"
	HealthFail        Mode = "health_fail"
)

type State struct {
	mu      sync.RWMutex
	mode    Mode
	delayMS int
}

func NewState() *State {
	return &State{
		mode:    None,
		delayMS: 1500,
	}
}

func (s *State) Get() (Mode, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mode, s.delayMS
}

func (s *State) Set(mode Mode, delayMS int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode = mode
	if delayMS > 0 {
		s.delayMS = delayMS
	}
}
