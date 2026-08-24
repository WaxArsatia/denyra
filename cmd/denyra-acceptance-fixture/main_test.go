package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFixtureServesSFTPGoToken(t *testing.T) {
	server := httptest.NewServer(newFixture("").routes())
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/api/v2/token")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var payload struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil || payload.AccessToken != "acceptance-token" || response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d payload=%+v err=%v", response.StatusCode, payload, err)
	}
}
