package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/waxarsatia/denyra/internal/pipeline/application"
)

func TestConfirmMigrationsRequiresExplicitConfirmation(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/migration-batches/batch-1/confirm", strings.NewReader(url.Values{
		"item_id": {"item-1"},
	}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()

	(Console{}).confirmMigrations(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestMigrationBatchStatusReturnsNoContentWhenRevisionIsUnchanged(t *testing.T) {
	reader := migrationStatusReader{status: application.MigrationBatchStatus{State: "RUNNING", Active: 2, Completed: 3, Failed: 1, Revision: 7}}
	request := httptest.NewRequest(http.MethodGet, "/migration-batches/batch-1/status?revision=7", nil)
	request.SetPathValue("batchID", "batch-1")
	response := httptest.NewRecorder()

	(Console{dependencies: Dependencies{MigrationReader: reader}}).migrationBatchStatus(response, request)

	if response.Code != http.StatusNoContent || response.Body.Len() != 0 || response.Header().Get("HX-Trigger") != "" {
		t.Fatalf("status=%d trigger=%q body=%q", response.Code, response.Header().Get("HX-Trigger"), response.Body.String())
	}
}

func TestMigrationBatchStatusReturnsSmallChangedFragment(t *testing.T) {
	reader := migrationStatusReader{status: application.MigrationBatchStatus{State: "RUNNING", Active: 1, Completed: 4, Failed: 2, Revision: 8}}
	request := httptest.NewRequest(http.MethodGet, "/migration-batches/batch-1/status?revision=7", nil)
	request.SetPathValue("batchID", "batch-1")
	response := httptest.NewRecorder()

	(Console{dependencies: Dependencies{MigrationReader: reader}}).migrationBatchStatus(response, request)

	body := response.Body.String()
	if response.Code != http.StatusOK || response.Header().Get("HX-Trigger") != "migration-batch-changed" || !strings.Contains(body, "Active 1") || !strings.Contains(body, "Completed 4") || !strings.Contains(body, "Failed 2") || !strings.Contains(body, "every 5s") {
		t.Fatalf("status=%d trigger=%q body=%q", response.Code, response.Header().Get("HX-Trigger"), body)
	}
}

func TestMigrationBatchStatusStopsPollingWhenTerminal(t *testing.T) {
	reader := migrationStatusReader{status: application.MigrationBatchStatus{State: "COMPLETED", Completed: 6, Revision: 9}}
	request := httptest.NewRequest(http.MethodGet, "/migration-batches/batch-1/status?revision=8", nil)
	request.SetPathValue("batchID", "batch-1")
	response := httptest.NewRecorder()

	(Console{dependencies: Dependencies{MigrationReader: reader}}).migrationBatchStatus(response, request)

	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "every 5s") {
		t.Fatalf("terminal status=%d body=%q", response.Code, response.Body.String())
	}
}

type migrationStatusReader struct {
	status application.MigrationBatchStatus
}

func (r migrationStatusReader) UnmanagedSummaries(context.Context, application.UnmanagedFilter, int, string) ([]application.UnmanagedSummary, string, error) {
	return nil, "", nil
}
func (r migrationStatusReader) MigrationBatchDetail(context.Context, string) (application.MigrationBatchDetail, error) {
	return application.MigrationBatchDetail{}, nil
}
func (r migrationStatusReader) MigrationBatchStatus(context.Context, string) (application.MigrationBatchStatus, error) {
	return r.status, nil
}

func TestRetryMigrationRejectsInvalidRevision(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/migration-items/item-1/retry", strings.NewReader(url.Values{
		"state_revision": {"not-a-revision"},
	}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()

	(Console{}).retryMigration(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}
