package spotiflac

import (
	"time"

	"github.com/waxarsatia/denyra/internal/gateway/domain"
)

type RunRequest struct {
	JobID           string
	ReleaseGroupID  string
	SelectedRelease string
	OutputDirectory string
	Providers       []string
	OverallDeadline time.Time
}

type OutputFile struct {
	Path, SHA256 string
	Size         int64
}

type ProviderExecution struct {
	Provider                         string
	Outcome                          domain.ProviderOutcome
	StartedAt                        time.Time
	EstablishedAt, CompletedAt       *time.Time
	Command                          []string
	ExitCode                         int
	Signal, ErrorClass, ErrorMessage string
	Stdout, Stderr                   string
	Output                           []OutputFile
}

type RunResult struct {
	EngineVersion, EngineSHA256, RegistryCommit, NodeVersion, NodeArtifactSHA256 string
	Extensions                                                                   []ExtensionIdentity
	Providers                                                                    []ProviderExecution
	WinningProvider                                                              string
	Output                                                                       []OutputFile
	StartedAt, CompletedAt                                                       time.Time
}

func (result RunResult) DomainResults() []domain.ProviderResult {
	values := make([]domain.ProviderResult, 0, len(result.Providers))
	for _, provider := range result.Providers {
		evidence := provider.ErrorMessage
		if evidence == "" {
			evidence = string(provider.Outcome)
		}
		values = append(values, domain.ProviderResult{Provider: provider.Provider, Outcome: provider.Outcome, Evidence: evidence})
	}
	return values
}
