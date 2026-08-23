package domain

import (
	"fmt"
	"time"
)

type RetryPolicy struct {
	Primary, Fallback []time.Duration
	NoCandidate       time.Duration
}

func (p RetryPolicy) Validate() error {
	for name, values := range map[string][]time.Duration{"primary": p.Primary, "fallback": p.Fallback} {
		if len(values) == 0 {
			return fmt.Errorf("%s retry schedule is empty", name)
		}
		for i, value := range values {
			if value <= 0 || (i > 0 && value < values[i-1]) {
				return fmt.Errorf("%s retry schedule is invalid", name)
			}
		}
	}
	if p.NoCandidate <= 0 {
		return fmt.Errorf("no-candidate retry must be positive")
	}
	return nil
}
func Deadline(now time.Time, attempt int, schedule []time.Duration) (time.Time, error) {
	if attempt < 0 || len(schedule) == 0 {
		return time.Time{}, fmt.Errorf("invalid retry attempt or schedule")
	}
	index := attempt
	if index >= len(schedule) {
		index = len(schedule) - 1
	}
	if schedule[index] <= 0 {
		return time.Time{}, fmt.Errorf("retry duration must be positive")
	}
	return now.UTC().Add(schedule[index]), nil
}
func (p RetryPolicy) PrimaryDeadline(now time.Time, attempt int) (time.Time, error) {
	return Deadline(now, attempt, p.Primary)
}
func (p RetryPolicy) FallbackDeadline(now time.Time, attempt int) (time.Time, error) {
	return Deadline(now, attempt, p.Fallback)
}
func (p RetryPolicy) NoCandidateDeadline(now time.Time) (time.Time, error) {
	if err := p.Validate(); err != nil {
		return time.Time{}, err
	}
	return now.UTC().Add(p.NoCandidate), nil
}
