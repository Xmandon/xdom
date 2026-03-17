package app

import "sync"

type FaultMode string

const (
	FaultNone       FaultMode = "none"
	FaultTimeout    FaultMode = "timeout"
	FaultError500   FaultMode = "error500"
	FaultPanic      FaultMode = "panic"
	FaultHealthFail FaultMode = "health_fail"
)

type FaultState struct {
	mu      sync.RWMutex
	mode    FaultMode
	delayMS int
}

func NewFaultState() *FaultState {
	return &FaultState{
		mode:    FaultNone,
		delayMS: 1500,
	}
}

func (f *FaultState) Get() (FaultMode, int) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.mode, f.delayMS
}

func (f *FaultState) Set(mode FaultMode, delayMS int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mode = mode
	if delayMS > 0 {
		f.delayMS = delayMS
	}
}
