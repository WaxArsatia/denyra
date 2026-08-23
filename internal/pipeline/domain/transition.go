package domain

import (
	"fmt"
	"strings"
	"time"
)

type TransitionEvent struct {
	CandidateID      string
	Actor            string
	Reason           string
	PreviousState    State
	NewState         State
	PreviousRevision uint64
	Revision         uint64
	OccurredAt       time.Time
}

var allowedTransitions = map[State]map[State]struct{}{
	StateReceived:            edges(StateClaimed, StateCancelled),
	StateClaimed:             edges(StateStabilizing, StateCancelled),
	StateStabilizing:         edges(StateWorking, StateWaitingResubmit, StateQuarantined, StateCancelled),
	StateWaitingResubmit:     edges(StateReceived, StateCancelled),
	StateWorking:             edges(StateTechnicalValidation, StateQuarantined, StateCancelled),
	StateTechnicalValidation: edges(StateReleaseMatching, StateQuarantined, StateRejected, StateCancelled),
	StateReleaseMatching:     edges(StateReviewRequired, StateEnriching, StateQuarantined, StateRejected, StateCancelled),
	StateReviewRequired:      edges(StateWorking, StateRejected, StateCancelled),
	StateEnriching:           edges(StateApproved, StateQuarantined, StateCancelled),
	StateApproved:            edges(StateArbitrationPending, StateImportReady, StateSuperseded, StateCancelled),
	StateArbitrationPending:  edges(StateImportReady, StateSuperseded, StateCancelled),
	StateImportReady:         edges(StateImportSubmitted, StateSuperseded, StateCancelled),
	StateImportSubmitted:     edges(StateImportReconciling),
	StateImportReconciling:   edges(StateImported, StateImportSubmitted),
	StateQuarantined:         edges(StateReviewRequired, StateRejected, StateCancelled),
}

func edges(states ...State) map[State]struct{} {
	result := make(map[State]struct{}, len(states))
	for _, state := range states {
		result[state] = struct{}{}
	}
	return result
}

func CanTransition(from, to State) bool {
	_, ok := allowedTransitions[from][to]
	return ok
}

func (c *Candidate) Transition(expectedRevision uint64, to State, actor, reason string, now time.Time) (TransitionEvent, error) {
	if expectedRevision != c.StateRevision {
		return TransitionEvent{}, &StaleRevisionError{Expected: expectedRevision, Current: c.StateRevision, State: c.State}
	}
	if !c.State.Valid() || !to.Valid() || !CanTransition(c.State, to) {
		return TransitionEvent{}, &IllegalTransitionError{From: c.State, To: to}
	}
	if strings.TrimSpace(actor) == "" || strings.TrimSpace(reason) == "" {
		return TransitionEvent{}, fmt.Errorf("transition actor and reason are required")
	}
	if now.IsZero() || now.Location() != time.UTC || now.Before(c.UpdatedAt) {
		return TransitionEvent{}, fmt.Errorf("transition timestamp must be monotonic UTC")
	}
	previousState, previousRevision := c.State, c.StateRevision
	c.State = to
	c.StateRevision++
	c.UpdatedAt = now
	return TransitionEvent{
		CandidateID: c.ID, Actor: strings.TrimSpace(actor), Reason: strings.TrimSpace(reason),
		PreviousState: previousState, NewState: to, PreviousRevision: previousRevision,
		Revision: c.StateRevision, OccurredAt: now,
	}, nil
}
