package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAcquisitionEvidenceUsesAuthenticatedBoundedReadOnlyRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.EscapedPath() != "/internal/acquisitions/job%2Fone" {
			t.Errorf("request = %s %s", request.Method, request.URL.EscapedPath())
		}
		if request.Header.Get("Authorization") != "Bearer internal-secret" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"job":{"job_id":"job/one","state":"ARBITRATING","state_revision":7,"lidarr_album_id":42,"release_group_mbid":"12345678-1234-1234-1234-123456789abc","updated_at":"2026-08-24T10:00:00Z"},"transitions":[],"attempts":[{"id":"attempt-1","kind":"SPOTIFLAC","provider":"tidal-web","outcome":"RETRYABLE_ERROR","error_class":"PROCESS_EXIT","message":"password=visible-secret","number":1,"started_at":"2026-08-24T10:00:00Z","stderr":"must-not-cross","command":["must-not-cross"]}],"candidates":[],"correlation":[]}`)
	}))
	defer server.Close()

	evidence, err := (Client{BaseURL: server.URL, Bearer: "internal-secret", HTTP: server.Client(), ResponseLimit: 4096}).AcquisitionEvidence(context.Background(), "job/one")
	if err != nil {
		t.Fatal(err)
	}
	if evidence.JobID != "job/one" || evidence.State != "ARBITRATING" || evidence.Revision != 7 || evidence.AlbumID != 42 || len(evidence.Attempts) != 1 || evidence.Attempts[0].Message != "password=[REDACTED]" {
		t.Fatalf("evidence=%+v", evidence)
	}
}

func TestListAcquisitionsEscapesFiltersAndPropagatesCursor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/internal/acquisitions" || request.URL.Query().Get("limit") != "50" || request.URL.Query().Get("cursor") != "cursor +/=" || request.URL.Query().Get("state") != "NO_CANDIDATE" {
			t.Errorf("request = %s %s", request.Method, request.URL.String())
		}
		if request.Header.Get("Authorization") != "Bearer internal-secret" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		fmt.Fprint(writer, `{"items":[{"job_id":"job-1","state":"NO_CANDIDATE","release_group_mbid":"12345678-1234-1234-1234-123456789abc","lidarr_album_id":42,"state_revision":3,"primary_attempt":2,"fallback_attempt":1,"updated_at":"2026-08-24T10:00:00Z"}],"next":"next-cursor"}`)
	}))
	defer server.Close()
	page, err := (Client{BaseURL: server.URL, Bearer: "internal-secret", HTTP: server.Client(), ResponseLimit: 4096}).ListAcquisitions(context.Background(), 50, "cursor +/=", "NO_CANDIDATE")
	if err != nil || len(page.Items) != 1 || page.Items[0].JobID != "job-1" || page.Next != "next-cursor" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
}

func TestAcquisitionEvidenceRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(writer, `{"oversized":"payload"}`)
	}))
	defer server.Close()
	_, err := (Client{BaseURL: server.URL, Bearer: "secret", HTTP: server.Client(), ResponseLimit: 4}).AcquisitionEvidence(context.Background(), "job")
	if err == nil || !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatal("oversized gateway evidence was accepted")
	}
}
