package domain

import "fmt"

type StaleRevisionError struct {
	Expected uint64
	Current  uint64
	State    State
}

func (e *StaleRevisionError) Error() string {
	return fmt.Sprintf("stale candidate revision: expected %d, current %d in %s", e.Expected, e.Current, e.State)
}

type IllegalTransitionError struct {
	From State
	To   State
}

func (e *IllegalTransitionError) Error() string {
	return fmt.Sprintf("illegal candidate transition %s -> %s", e.From, e.To)
}
