package domain

import "fmt"

type State string

const (
	StateReceived            State = "RECEIVED"
	StateClaimed             State = "CLAIMED"
	StateStabilizing         State = "STABILIZING"
	StateWaitingResubmit     State = "WAITING_RESUBMIT"
	StateWorking             State = "WORKING"
	StateTechnicalValidation State = "TECHNICAL_VALIDATION"
	StateReleaseMatching     State = "RELEASE_MATCHING"
	StateReviewRequired      State = "REVIEW_REQUIRED"
	StateEnriching           State = "ENRICHING"
	StateApproved            State = "APPROVED"
	StateArbitrationPending  State = "ARBITRATION_PENDING"
	StateImportReady         State = "IMPORT_READY"
	StateImportSubmitted     State = "IMPORT_SUBMITTED"
	StateImportReconciling   State = "IMPORT_RECONCILING"
	StateImported            State = "IMPORTED"
	StateQuarantined         State = "QUARANTINED"
	StateRejected            State = "REJECTED"
	StateSuperseded          State = "SUPERSEDED"
	StateCancelled           State = "CANCELLED"
)

var allStates = map[State]struct{}{
	StateReceived: {}, StateClaimed: {}, StateStabilizing: {}, StateWaitingResubmit: {},
	StateWorking: {}, StateTechnicalValidation: {}, StateReleaseMatching: {}, StateReviewRequired: {},
	StateEnriching: {}, StateApproved: {}, StateArbitrationPending: {}, StateImportReady: {},
	StateImportSubmitted: {}, StateImportReconciling: {}, StateImported: {}, StateQuarantined: {},
	StateRejected: {}, StateSuperseded: {}, StateCancelled: {},
}

func ParseState(value string) (State, error) {
	state := State(value)
	if _, ok := allStates[state]; !ok {
		return "", fmt.Errorf("unknown candidate state %q", value)
	}
	return state, nil
}

func (s State) Valid() bool {
	_, ok := allStates[s]
	return ok
}

func (s State) Terminal() bool {
	switch s {
	case StateImported, StateRejected, StateSuperseded, StateCancelled:
		return true
	default:
		return false
	}
}
