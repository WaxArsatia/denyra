package domain

import (
	"fmt"
	"time"
)

type Job struct {
	ID                   string
	LidarrAlbumID        int64
	ReleaseGroupMBID     string
	SelectedReleaseMBID  string
	State                State
	Revision             uint64
	PrimaryAttempt       int
	FallbackAttempt      int
	ConfigSnapshotID     string
	NextRetryAt          *time.Time
	CreatedAt, UpdatedAt time.Time
}
type Transition struct {
	JobID, Actor, Reason       string
	Previous, Next             State
	PreviousRevision, Revision uint64
	OccurredAt                 time.Time
}

func NewJob(id string, albumID int64, releaseGroup, configID string, now time.Time) (Job, error) {
	if id == "" || albumID <= 0 || configID == "" {
		return Job{}, fmt.Errorf("job identity is incomplete")
	}
	if _, err := CanonicalMBID(releaseGroup); err != nil {
		return Job{}, fmt.Errorf("release group: %w", err)
	}
	now = now.UTC()
	return Job{ID: id, LidarrAlbumID: albumID, ReleaseGroupMBID: releaseGroup, State: StateDiscovered, ConfigSnapshotID: configID, CreatedAt: now, UpdatedAt: now}, nil
}
func (j *Job) Transition(expected uint64, to State, actor, reason string, at time.Time) (Transition, error) {
	if expected != j.Revision {
		return Transition{}, &StaleRevisionError{Expected: expected, Current: j.Revision, State: j.State}
	}
	if actor == "" || reason == "" || at.IsZero() {
		return Transition{}, fmt.Errorf("transition audit fields are required")
	}
	if !CanTransition(j.State, to) {
		return Transition{}, fmt.Errorf("illegal acquisition transition %s -> %s", j.State, to)
	}
	event := Transition{JobID: j.ID, Actor: actor, Reason: reason, Previous: j.State, Next: to, PreviousRevision: j.Revision, Revision: j.Revision + 1, OccurredAt: at.UTC()}
	j.State = to
	j.Revision++
	j.UpdatedAt = at.UTC()
	return event, nil
}
func (j Job) DedupKey() string { return fmt.Sprintf("%d:%s", j.LidarrAlbumID, j.ReleaseGroupMBID) }

type StaleRevisionError struct {
	Expected, Current uint64
	State             State
}

func (e *StaleRevisionError) Error() string {
	return fmt.Sprintf("stale acquisition revision: expected=%d current=%d state=%s", e.Expected, e.Current, e.State)
}
