package gateway_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/gateway/adapters/lidarr"
	"github.com/waxarsatia/denyra/internal/gateway/application"
	"github.com/waxarsatia/denyra/internal/gateway/domain"
	"github.com/waxarsatia/denyra/internal/gateway/persistence"
)

const releaseMBID = "abcdefab-1234-5678-9abc-abcdefabcdef"

type advancingClock struct {
	value time.Time
}

func (c *advancingClock) Now() time.Time { return c.value }
func (c *advancingClock) Pause(_ context.Context, duration time.Duration) error {
	c.value = c.value.Add(duration)
	return nil
}

func TestPrimarySearchPersistsWatermarksDeadlinesAndIntent(t *testing.T) {
	db, repositories, now := gatewayRepositories(t)
	defer db.Close()
	job := createJob(t, repositories, now)

	var commandPolls int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.Contains(request.URL.Path, "slskd") {
			t.Errorf("gateway attempted direct slskd access: %s", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/queue":
			fmt.Fprint(writer, `{"records":[{"id":11}],"totalRecords":1}`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/history":
			fmt.Fprint(writer, `{"records":[{"id":21}],"totalRecords":1}`)
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/command":
			var body struct {
				Name     string  `json:"name"`
				AlbumIDs []int64 `json:"albumIds"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode command: %v", err)
			}
			if body.Name != "AlbumSearch" || len(body.AlbumIDs) != 1 || body.AlbumIDs[0] != 42 {
				t.Errorf("unexpected command: %+v", body)
			}
			fmt.Fprint(writer, `{"id":77,"name":"AlbumSearch","status":"queued"}`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/command/77":
			commandPolls++
			status := "started"
			if commandPolls > 1 {
				status = "completed"
			}
			fmt.Fprintf(writer, `{"id":77,"name":"AlbumSearch","status":%q}`, status)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	clock := &advancingClock{value: now}
	service := application.PrimarySearch{
		Lidarr: lidarr.Client{BaseURL: server.URL, APIKey: "test-key", HTTP: server.Client(), ResponseLimit: 1 << 20},
		Store:  repositories,
		Policy: domain.RetryPolicy{
			Primary:     []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour, 6 * time.Hour},
			Fallback:    []time.Duration{5 * time.Minute},
			NoCandidate: 24 * time.Hour,
		},
		CommandTimeout: 10 * time.Minute,
		PollInterval:   2 * time.Second,
		GraceWindow:    time.Minute,
		Pause:          clock.Pause,
		Now:            clock.Now,
	}
	if err := service.Run(context.Background(), job.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	stored, err := repositories.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != domain.StatePrimaryReconciling || stored.Revision != 3 || stored.PrimaryAttempt != 0 {
		t.Fatalf("stored job = %+v", stored)
	}
	var queueWatermark, historyWatermark, commandID, startedText, commandDeadlineText, graceDeadlineText string
	if err := db.QueryRow(`SELECT queue_watermark,history_watermark,command_id,correlation_started_at,command_deadline,grace_deadline FROM acquisition_jobs WHERE id=?`, job.ID).Scan(&queueWatermark, &historyWatermark, &commandID, &startedText, &commandDeadlineText, &graceDeadlineText); err != nil {
		t.Fatal(err)
	}
	started, _ := time.Parse(time.RFC3339Nano, startedText)
	commandDeadline, _ := time.Parse(time.RFC3339Nano, commandDeadlineText)
	graceDeadline, _ := time.Parse(time.RFC3339Nano, graceDeadlineText)
	if queueWatermark != "11" || historyWatermark != "21" || commandID != "77" {
		t.Fatalf("search identity = queue=%q history=%q command=%q", queueWatermark, historyWatermark, commandID)
	}
	if commandDeadline.Sub(started) != 10*time.Minute {
		t.Fatalf("command window = %s", commandDeadline.Sub(started))
	}
	if graceDeadline.Sub(clock.Now()) != time.Minute {
		t.Fatalf("grace deadline = %s, current=%s", graceDeadline, clock.Now())
	}
	var effectStatus string
	if err := db.QueryRow(`SELECT status FROM external_effects WHERE job_id=? AND effect_type='ALBUM_SEARCH'`, job.ID).Scan(&effectStatus); err != nil {
		t.Fatal(err)
	}
	if effectStatus != "ACKNOWLEDGED" {
		t.Fatalf("effect status = %s", effectStatus)
	}
}

func TestPrimarySearchFailureIsRetryableAndNeverZeroResult(t *testing.T) {
	db, repositories, now := gatewayRepositories(t)
	defer db.Close()
	job := createJob(t, repositories, now)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/v1/queue", "/api/v1/history":
			fmt.Fprint(writer, `{"records":[],"totalRecords":0}`)
		case "/api/v1/command":
			if request.Method == http.MethodPost {
				fmt.Fprint(writer, `{"id":88,"name":"AlbumSearch","status":"queued"}`)
				return
			}
			http.NotFound(writer, request)
		case "/api/v1/command/88":
			fmt.Fprint(writer, `{"id":88,"name":"AlbumSearch","status":"failed","message":"indexer unavailable"}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	clock := &advancingClock{value: now}
	service := application.PrimarySearch{
		Lidarr:         lidarr.Client{BaseURL: server.URL, APIKey: "test-key", HTTP: server.Client(), ResponseLimit: 1 << 20},
		Store:          repositories,
		Policy:         domain.RetryPolicy{Primary: []time.Duration{time.Minute, 5 * time.Minute}, Fallback: []time.Duration{5 * time.Minute}, NoCandidate: 24 * time.Hour},
		CommandTimeout: 10 * time.Minute,
		PollInterval:   2 * time.Second,
		GraceWindow:    time.Minute,
		Pause:          clock.Pause,
		Now:            clock.Now,
	}
	if err := service.Run(context.Background(), job.ID); err == nil {
		t.Fatal("failed AlbumSearch reported success")
	}
	stored, err := repositories.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != domain.StatePrimaryRetryableError || stored.PrimaryAttempt != 1 || stored.NextRetryAt == nil || !stored.NextRetryAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("retryable job = %+v", stored)
	}
}

func TestWantedDiscoveryDeduplicatesAndRevisesSelectedRelease(t *testing.T) {
	db, repositories, now := gatewayRepositories(t)
	defer db.Close()

	selected := releaseMBID
	var mutex sync.RWMutex
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/wanted/missing" {
			http.NotFound(writer, request)
			return
		}
		mutex.RLock()
		current := selected
		mutex.RUnlock()
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(writer, `{"records":[{"id":42,"foreignAlbumId":%q,"releaseId":%q,"monitored":true}],"totalRecords":1}`, releaseGroupMBID, current)
	}))
	defer server.Close()
	client := lidarr.Client{BaseURL: server.URL, APIKey: "test-key", HTTP: server.Client(), ResponseLimit: 1 << 20}
	service := application.WantedDiscovery{Lidarr: client, Store: repositories, ConfigSnapshotID: "config-1", Now: func() time.Time { return now }}

	if changed, err := service.Reconcile(context.Background()); err != nil || changed != 1 {
		t.Fatalf("first reconcile = %d, %v", changed, err)
	}
	if changed, err := service.Reconcile(context.Background()); err != nil || changed != 0 {
		t.Fatalf("deduplicated reconcile = %d, %v", changed, err)
	}
	mutex.Lock()
	selected = "11111111-2222-3333-4444-555555555555"
	mutex.Unlock()
	if changed, err := service.Reconcile(context.Background()); err != nil || changed != 1 {
		t.Fatalf("release revision reconcile = %d, %v", changed, err)
	}
	job, err := repositories.FindActiveJob(context.Background(), 42, releaseGroupMBID)
	if err != nil {
		t.Fatal(err)
	}
	if job.SelectedReleaseMBID != selected || job.Revision != 1 || job.State != domain.StateDiscovered {
		t.Fatalf("revised job = %+v", job)
	}
	var transitions int
	if err := db.QueryRow(`SELECT COUNT(*) FROM state_transitions WHERE job_id=? AND reason='selected MusicBrainz release changed'`, job.ID).Scan(&transitions); err != nil {
		t.Fatal(err)
	}
	if transitions != 1 {
		t.Fatalf("release revision audit transitions = %d", transitions)
	}
}

func TestWantedDiscoveryBackfillsMissingSelectedReleaseWithoutRestartingActiveAcquisition(t *testing.T) {
	db, repositories, now := gatewayRepositories(t)
	defer db.Close()
	job := createJob(t, repositories, now)
	preparePrimaryReconciliation(t, repositories, job, now)
	job, err := repositories.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repositories.UpdateState(context.Background(), persistence.TransitionCommand{
		JobID: job.ID, Expected: job.Revision, To: domain.StatePrimaryActive,
		Actor: "test", Reason: "correlated primary grab", OccurredAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(writer, `{"records":[{"id":42,"foreignAlbumId":%q,"monitored":true,"releases":[{"foreignReleaseId":%q,"monitored":true}]}],"totalRecords":1}`, releaseGroupMBID, releaseMBID)
	}))
	defer server.Close()
	service := application.WantedDiscovery{
		Lidarr:           lidarr.Client{BaseURL: server.URL, APIKey: "test-key", HTTP: server.Client(), ResponseLimit: 1 << 20},
		Store:            repositories,
		ConfigSnapshotID: "config-1",
		Now:              func() time.Time { return now.Add(time.Minute) },
	}

	if changed, err := service.Reconcile(context.Background()); err != nil || changed != 1 {
		t.Fatalf("backfill reconcile=%d err=%v", changed, err)
	}
	stored, err := repositories.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.SelectedReleaseMBID != releaseMBID {
		t.Fatalf("selected release=%q", stored.SelectedReleaseMBID)
	}
	if stored.State != domain.StatePrimaryActive {
		t.Fatalf("state=%s, want %s", stored.State, domain.StatePrimaryActive)
	}
}
