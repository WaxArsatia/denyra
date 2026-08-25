package domain

import "fmt"

type State string

const (
	StateDiscovered             State = "DISCOVERED"
	StatePrimarySearchRequested State = "PRIMARY_SEARCH_REQUESTED"
	StatePrimarySearchRunning   State = "PRIMARY_SEARCH_RUNNING"
	StatePrimaryReconciling     State = "PRIMARY_RECONCILING"
	StatePrimaryActive          State = "PRIMARY_ACTIVE"
	StatePrimaryRetryableError  State = "PRIMARY_RETRYABLE_ERROR"
	StateFallbackRunning        State = "FALLBACK_RUNNING"
	StateFallbackRetryableError State = "FALLBACK_RETRYABLE_ERROR"
	StateNoCandidate            State = "NO_CANDIDATE"
	StateDualCandidate          State = "DUAL_CANDIDATE"
	StateArbitrating            State = "ARBITRATING"
	StateWinnerLocked           State = "WINNER_LOCKED"
	StateHandedOff              State = "HANDED_OFF"
	StateCancelled              State = "CANCELLED"
)

var states = map[State]struct{}{
	StateDiscovered:             {},
	StatePrimarySearchRequested: {},
	StatePrimarySearchRunning:   {},
	StatePrimaryReconciling:     {},
	StatePrimaryActive:          {},
	StatePrimaryRetryableError:  {},
	StateFallbackRunning:        {},
	StateFallbackRetryableError: {},
	StateNoCandidate:            {},
	StateDualCandidate:          {},
	StateArbitrating:            {},
	StateWinnerLocked:           {},
	StateHandedOff:              {},
	StateCancelled:              {},
}

func (s State) Valid() bool { _, ok := states[s]; return ok }
func ParseState(value string) (State, error) {
	state := State(value)
	if !state.Valid() {
		return "", fmt.Errorf("invalid acquisition state %q", value)
	}
	return state, nil
}
func (s State) Terminal() bool { return s == StateHandedOff || s == StateCancelled }

var transitions = map[State]map[State]bool{
	StateDiscovered: {
		StatePrimarySearchRequested: true,
		StateCancelled:              true,
	},
	StatePrimarySearchRequested: {
		StatePrimarySearchRunning:  true,
		StatePrimaryActive:         true,
		StatePrimaryRetryableError: true,
		StateCancelled:             true,
	},
	StatePrimarySearchRunning: {
		StatePrimaryReconciling:    true,
		StatePrimaryRetryableError: true,
		StateCancelled:             true,
	},
	StatePrimaryReconciling: {
		StatePrimaryActive:         true,
		StateFallbackRunning:       true,
		StatePrimaryRetryableError: true,
		StateCancelled:             true,
	},
	StatePrimaryActive: {
		StateDualCandidate: true,
		StateArbitrating:   true,
		StateHandedOff:     true,
		StateCancelled:     true,
	},
	StatePrimaryRetryableError: {
		StatePrimarySearchRequested: true,
		StateCancelled:              true,
	},
	StateFallbackRunning: {
		StatePrimaryActive:          true,
		StatePrimaryRetryableError:  true,
		StateDualCandidate:          true,
		StateArbitrating:            true,
		StateFallbackRetryableError: true,
		StateNoCandidate:            true,
		StateCancelled:              true,
	},
	StateFallbackRetryableError: {
		StateFallbackRunning:        true,
		StatePrimaryRetryableError:  true,
		StatePrimarySearchRequested: true,
		StatePrimaryActive:          true,
		StateCancelled:              true,
	},
	StateNoCandidate: {
		StatePrimarySearchRequested: true,
		StatePrimaryActive:          true,
		StateCancelled:              true,
	},
	StateDualCandidate: {
		StateArbitrating: true,
		StateCancelled:   true,
	},
	StateArbitrating: {
		StateDualCandidate: true,
		StateWinnerLocked:  true,
		StateCancelled:     true,
	},
	StateWinnerLocked: {
		StateHandedOff: true,
	},
}

func CanTransition(from, to State) bool { return transitions[from][to] }
