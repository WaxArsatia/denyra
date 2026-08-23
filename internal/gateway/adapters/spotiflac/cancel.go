package spotiflac

import (
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type ProcessRegistry struct {
	mutex            sync.Mutex
	active           map[string]*exec.Cmd
	superseded       map[string]bool
	terminationGrace time.Duration
}

func NewProcessRegistry(terminationGrace time.Duration) *ProcessRegistry {
	return &ProcessRegistry{active: make(map[string]*exec.Cmd), superseded: make(map[string]bool), terminationGrace: terminationGrace}
}

func (registry *ProcessRegistry) Track(jobID string, command *exec.Cmd) error {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if registry.active[jobID] != nil {
		return fmt.Errorf("SpotiFLAC process already active for job %s", jobID)
	}
	registry.active[jobID] = command
	return nil
}

func (registry *ProcessRegistry) Untrack(jobID string, command *exec.Cmd) {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if registry.active[jobID] == command {
		delete(registry.active, jobID)
	}
}

func (registry *ProcessRegistry) CancelSuperseded(jobID string) error {
	registry.mutex.Lock()
	command := registry.active[jobID]
	if command != nil {
		registry.superseded[jobID] = true
	}
	grace := registry.terminationGrace
	registry.mutex.Unlock()
	if command == nil {
		return fmt.Errorf("no active SpotiFLAC process for job %s", jobID)
	}
	if grace <= 0 {
		return fmt.Errorf("process termination grace is invalid")
	}
	if err := signalProcessGroup(command, syscall.SIGTERM); err != nil {
		return err
	}
	go func() {
		timer := time.NewTimer(grace)
		defer timer.Stop()
		<-timer.C
		registry.mutex.Lock()
		stillActive := registry.active[jobID] == command
		registry.mutex.Unlock()
		if stillActive {
			_ = signalProcessGroup(command, syscall.SIGKILL)
		}
	}()
	return nil
}

func (registry *ProcessRegistry) WasSuperseded(jobID string) bool {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	return registry.superseded[jobID]
}

func (registry *ProcessRegistry) Active(jobID string) bool {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	return registry.active[jobID] != nil
}

func (registry *ProcessRegistry) Clear(jobID string) {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	delete(registry.superseded, jobID)
}
