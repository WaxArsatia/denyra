package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
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
