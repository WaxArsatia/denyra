// Package health reports process liveness and local-only readiness.
package health

import (
	"slices"
	"sync"

	"github.com/waxarsatia/denyra/internal/contracts"
)

type Service struct {
	mu           sync.RWMutex
	live         bool
	dependencies map[string]contracts.DependencyHealth
}

func New() *Service {
	return &Service{live: true, dependencies: make(map[string]contracts.DependencyHealth)}
}

func (s *Service) Set(dependency contracts.DependencyHealth) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dependencies[dependency.Name] = dependency
}

func (s *Service) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.live = false
}

func (s *Service) Snapshot() contracts.Health {
	s.mu.RLock()
	defer s.mu.RUnlock()
	dependencies := make([]contracts.DependencyHealth, 0, len(s.dependencies))
	ready := s.live
	for _, dependency := range s.dependencies {
		dependencies = append(dependencies, dependency)
		if dependency.Local && dependency.State != contracts.DependencyOK {
			ready = false
		}
	}
	slices.SortFunc(dependencies, func(left, right contracts.DependencyHealth) int {
		if left.Name < right.Name {
			return -1
		}
		if left.Name > right.Name {
			return 1
		}
		return 0
	})
	return contracts.Health{Live: s.live, Ready: ready, Dependencies: dependencies}
}
