package gateway_test

import (
	"context"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/gateway/adapters/spotiflac"
	"github.com/waxarsatia/denyra/internal/gateway/application"
	"github.com/waxarsatia/denyra/internal/gateway/domain"
	"github.com/waxarsatia/denyra/internal/gateway/persistence"
)

type fallbackRunner struct {
	result  spotiflac.RunResult
	request spotiflac.RunRequest
}

func (runner *fallbackRunner) Run(_ context.Context, request spotiflac.RunRequest) (spotiflac.RunResult, error) {
	runner.request = request
	return runner.result, nil
}

func TestSpotiFLACFallbackPersistsStrictOutcomeAndDeadline(t *testing.T) {
	providers := []string{"ext:tidal-web", "ext:qobuz-web", "ext:deezer"}
	tests := map[string]struct {
		outcomes     []domain.ProviderOutcome
		wantState    domain.State
		wantRetry    time.Duration
		wantAttempts int
	}{
		"candidate":                 {outcomes: []domain.ProviderOutcome{domain.OutcomeCandidate}, wantState: domain.StateArbitrating},
		"all legitimate no result":  {outcomes: []domain.ProviderOutcome{domain.OutcomeLegitimateNoResult, domain.OutcomeLegitimateNoResult, domain.OutcomeLegitimateNoResult}, wantState: domain.StateNoCandidate, wantRetry: 24 * time.Hour},
		"mixed no result and error": {outcomes: []domain.ProviderOutcome{domain.OutcomeLegitimateNoResult, domain.OutcomeRetryableError, domain.OutcomeLegitimateNoResult}, wantState: domain.StateFallbackRetryableError, wantRetry: 5 * time.Minute, wantAttempts: 1},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			db, repositories, now := gatewayRepositories(t)
			defer db.Close()
			job := createJob(t, repositories, now)
			if _, err := db.Exec(`UPDATE acquisition_jobs SET selected_release_mbid=? WHERE id=?`, releaseMBID, job.ID); err != nil {
				t.Fatal(err)
			}
			prepareFallbackState(t, repositories, job, now)
			result := spotiflac.RunResult{StartedAt: now, CompletedAt: now.Add(time.Second)}
			for index, outcome := range test.outcomes {
				completed := now.Add(time.Second)
				result.Providers = append(result.Providers, spotiflac.ProviderExecution{Provider: providers[index], Outcome: outcome, StartedAt: now, CompletedAt: &completed, ExitCode: 0})
			}
			runner := &fallbackRunner{result: result}
			service := application.FallbackService{Runner: runner, Store: repositories, Policy: domain.RetryPolicy{Primary: []time.Duration{time.Minute}, Fallback: []time.Duration{5 * time.Minute, 15 * time.Minute, time.Hour, 6 * time.Hour}, NoCandidate: 24 * time.Hour}, Providers: providers, OutputRoot: "/data/downloads/spotiflac", OverallTimeout: 6 * time.Hour, Now: func() time.Time { return now }}
			if err := service.Run(context.Background(), job.ID); err != nil {
				t.Fatalf("Run: %v", err)
			}
			stored, err := repositories.Job(context.Background(), job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.State != test.wantState || stored.FallbackAttempt != test.wantAttempts || stored.OverallDeadline == nil || !stored.OverallDeadline.Equal(now.Add(6*time.Hour)) {
				t.Fatalf("stored job = %+v", stored)
			}
			if test.wantRetry == 0 {
				if stored.NextRetryAt != nil {
					t.Fatalf("unexpected retry: %s", stored.NextRetryAt)
				}
			} else if stored.NextRetryAt == nil || !stored.NextRetryAt.Equal(now.Add(test.wantRetry)) {
				t.Fatalf("retry=%v want=%s", stored.NextRetryAt, now.Add(test.wantRetry))
			}
			if runner.request.OutputDirectory != "/data/downloads/spotiflac/job-1" || runner.request.SelectedRelease != releaseMBID {
				t.Fatalf("runner request = %+v", runner.request)
			}
			var effectStatus string
			if err := db.QueryRow(`SELECT status FROM external_effects WHERE job_id=? AND effect_type='SPOTIFLAC_RUN'`, job.ID).Scan(&effectStatus); err != nil {
				t.Fatal(err)
			}
			if effectStatus != "ACKNOWLEDGED" {
				t.Fatalf("effect status = %s", effectStatus)
			}
			var providerCount int
			if err := db.QueryRow(`SELECT COUNT(*) FROM provider_results WHERE job_id=?`, job.ID).Scan(&providerCount); err != nil {
				t.Fatal(err)
			}
			if providerCount != len(test.outcomes) {
				t.Fatalf("provider evidence count=%d", providerCount)
			}
		})
	}
}

func TestSpotiFLACFallbackReusesPersistedOverallDeadline(t *testing.T) {
	db, repositories, now := gatewayRepositories(t)
	defer db.Close()
	job := createJob(t, repositories, now)
	if _, err := db.Exec(`UPDATE acquisition_jobs SET selected_release_mbid=? WHERE id=?`, releaseMBID, job.ID); err != nil {
		t.Fatal(err)
	}
	prepareFallbackState(t, repositories, job, now)
	persisted := now.Add(2 * time.Hour)
	if _, err := repositories.SetOverallDeadline(context.Background(), job.ID, 4, persisted, now); err != nil {
		t.Fatal(err)
	}
	completed := now.Add(time.Second)
	runner := &fallbackRunner{result: spotiflac.RunResult{StartedAt: now, CompletedAt: completed, Providers: []spotiflac.ProviderExecution{{Provider: "ext:tidal-web", Outcome: domain.OutcomeCandidate, StartedAt: now, CompletedAt: &completed}}}}
	service := application.FallbackService{Runner: runner, Store: repositories, Policy: domain.RetryPolicy{Primary: []time.Duration{time.Minute}, Fallback: []time.Duration{5 * time.Minute}, NoCandidate: 24 * time.Hour}, Providers: []string{"ext:tidal-web"}, OutputRoot: "/data/downloads/spotiflac", OverallTimeout: 6 * time.Hour, Now: func() time.Time { return now.Add(time.Hour) }}
	if err := service.Run(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	if !runner.request.OverallDeadline.Equal(persisted) {
		t.Fatalf("deadline=%s want=%s", runner.request.OverallDeadline, persisted)
	}
}

func prepareFallbackState(t *testing.T, repositories *persistence.Repositories, job domain.Job, now time.Time) {
	t.Helper()
	states := []domain.State{domain.StatePrimarySearchRequested, domain.StatePrimarySearchRunning, domain.StatePrimaryReconciling, domain.StateFallbackRunning}
	revision := job.Revision
	for _, state := range states {
		event, err := repositories.UpdateState(context.Background(), persistence.TransitionCommand{JobID: job.ID, Expected: revision, To: state, Actor: "test", Reason: "prepare fallback", OccurredAt: now})
		if err != nil {
			t.Fatal(err)
		}
		revision = event.Revision
	}
}
