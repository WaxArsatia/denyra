package harness

import (
	"errors"
	"sync"
)

type FaultBoundary struct {
	mu       sync.Mutex
	Failures map[string]error
	Calls    map[string]int
}

func NewFaultBoundary() *FaultBoundary {
	return &FaultBoundary{Failures: make(map[string]error), Calls: make(map[string]int)}
}

func (boundary *FaultBoundary) Invoke(name string) error {
	boundary.mu.Lock()
	defer boundary.mu.Unlock()
	boundary.Calls[name]++
	if err := boundary.Failures[name]; err != nil {
		return err
	}
	return nil
}

func (boundary *FaultBoundary) Fail(name string) {
	boundary.mu.Lock()
	defer boundary.mu.Unlock()
	boundary.Failures[name] = errors.New("injected acceptance fault")
}

func (boundary *FaultBoundary) Clear(name string) {
	boundary.mu.Lock()
	defer boundary.mu.Unlock()
	delete(boundary.Failures, name)
}
