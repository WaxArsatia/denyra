package harness

import (
	"errors"
	"fmt"
	"os"
	"strings"
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

func InjectUpdateHealthFailure(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	startMarker := "  media-pipeline:\n"
	endMarker := "\n  acquisition-gateway:\n"
	start := strings.Index(string(content), startMarker)
	if start < 0 {
		return fmt.Errorf("acceptance media-pipeline service block is missing")
	}
	endOffset := strings.Index(string(content)[start+len(startMarker):], endMarker)
	if endOffset < 0 {
		return fmt.Errorf("acceptance media-pipeline service block is missing")
	}
	end := start + len(startMarker) + endOffset
	block := string(content)[start:end]
	if strings.Count(block, "    environment:\n") != 1 || strings.Count(block, "    depends_on:\n") != 1 {
		return fmt.Errorf("acceptance media-pipeline service structure is unexpected")
	}
	block = strings.Replace(block, "    environment:\n", "    environment:\n      DENYRA_ACCEPTANCE_FAIL_HEALTH: \"${DENYRA_ACCEPTANCE_FAIL_HEALTH:-0}\"\n", 1)
	healthcheck := `    healthcheck:
      interval: 1s
      timeout: 1s
      retries: 2
      start_period: 1s
      test: ["CMD-SHELL", "if [ \"$$DENYRA_ACCEPTANCE_FAIL_HEALTH\" = \"1\" ]; then exit 1; else /app/media-pipeline healthcheck --address 127.0.0.1:8081; fi"]
`
	block = strings.Replace(block, "    depends_on:\n", healthcheck+"    depends_on:\n", 1)
	updated := string(content)[:start] + block + string(content)[end:]
	return os.WriteFile(path, []byte(updated), 0o644)
}
