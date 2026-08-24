package internalapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestQualityClientTargetsCandidateSpecificApprovalRoute(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		path = request.URL.Path
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusAccepted)
		_, _ = writer.Write([]byte(`{"status":"accepted"}`))
	}))
	defer server.Close()

	client := QualityClient{BaseURL: server.URL, Bearer: "secret", HTTP: server.Client(), ResponseLimit: 1 << 20}
	payload := []byte(`{"candidate_id":"candidate-123"}`)
	if _, err := client.ReportApproved(context.Background(), payload, "request-123", "quality-candidate-123"); err != nil {
		t.Fatalf("report approved: %v", err)
	}
	if path != "/internal/candidates/candidate-123/approved" {
		t.Fatalf("callback path=%q", path)
	}
}
