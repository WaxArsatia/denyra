package contracts

type DependencyState string

const (
	DependencyOK       DependencyState = "ok"
	DependencyDegraded DependencyState = "degraded"
	DependencyFailed   DependencyState = "failed"
)

type DependencyHealth struct {
	Name    string          `json:"name"`
	State   DependencyState `json:"state"`
	Details string          `json:"details,omitempty"`
	Local   bool            `json:"local"`
}

type Health struct {
	Live         bool               `json:"live"`
	Ready        bool               `json:"ready"`
	Dependencies []DependencyHealth `json:"dependencies"`
}
