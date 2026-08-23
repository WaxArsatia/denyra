package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
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
		fmt.Fprint(writer, `{"job":{"job_id":"job/one","state":"ARBITRATING","state_revision":7,"lidarr_album_id":42,"release_group_mbid":"12345678-1234-1234-1234-123456789abc","updated_at":"2026-08-24T10:00:00Z"},"transitions":[],"attempts":[],"candidates":[],"correlation":[]}`)
	}))
	defer server.Close()

	evidence, err := (Client{BaseURL: server.URL, Bearer: "internal-secret", HTTP: server.Client(), ResponseLimit: 4096}).AcquisitionEvidence(context.Background(), "job/one")
	if err != nil {
		t.Fatal(err)
	}
	if evidence.JobID != "job/one" || evidence.State != "ARBITRATING" || evidence.Revision != 7 || evidence.AlbumID != 42 || len(evidence.Evidence) == 0 {
		t.Fatalf("evidence=%+v", evidence)
	}
}

func TestAcquisitionEvidenceRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(writer, `{"oversized":"payload"}`)
	}))
	defer server.Close()
	_, err := (Client{BaseURL: server.URL, Bearer: "secret", HTTP: server.Client(), ResponseLimit: 4}).AcquisitionEvidence(context.Background(), "job")
	if err == nil {
		t.Fatal("oversized gateway evidence was accepted")
	}
}
