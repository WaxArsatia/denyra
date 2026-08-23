package domain

import "fmt"

type ProviderOutcome string

const (
	OutcomeCandidate          ProviderOutcome = "CANDIDATE"
	OutcomeLegitimateNoResult ProviderOutcome = "LEGITIMATE_NO_RESULT"
	OutcomeRetryableError     ProviderOutcome = "RETRYABLE_ERROR"
)

type ProviderResult struct {
	Provider string
	Outcome  ProviderOutcome
	Evidence string
}

func ClassifyFallback(results []ProviderResult) (State, error) {
	if len(results) == 0 {
		return "", fmt.Errorf("provider results are empty")
	}
	allNoResult := true
	for _, result := range results {
		if result.Provider == "" || result.Evidence == "" {
			return "", fmt.Errorf("provider result evidence is incomplete")
		}
		switch result.Outcome {
		case OutcomeCandidate:
			return StateArbitrating, nil
		case OutcomeRetryableError:
			allNoResult = false
		case OutcomeLegitimateNoResult:
		default:
			return "", fmt.Errorf("unknown provider outcome %q", result.Outcome)
		}
	}
	if allNoResult {
		return StateNoCandidate, nil
	}
	return StateFallbackRetryableError, nil
}
