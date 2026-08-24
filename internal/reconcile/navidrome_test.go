package reconcile

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNavidromeCreatesFirstAdministrator(t *testing.T) {
	var received map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/auth/createAdmin" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	navidrome := Navidrome{BaseURL: server.URL, AdminPassword: "navidrome-secret", HTTP: server.Client()}
	outcome, err := navidrome.Apply(context.Background())
	if err != nil || !outcome.Changed || received["username"] != "admin" || received["password"] != "navidrome-secret" {
		t.Fatalf("outcome=%+v received=%v err=%v", outcome, received, err)
	}
}

func TestNavidromeAdoptsExistingAdministrator(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "a user already exists", http.StatusForbidden)
	}))
	defer server.Close()
	navidrome := Navidrome{BaseURL: server.URL, AdminPassword: "navidrome-secret", HTTP: server.Client()}
	outcome, err := navidrome.Apply(context.Background())
	if err != nil || outcome.Changed {
		t.Fatalf("outcome=%+v err=%v", outcome, err)
	}
}

func TestNavidromeFailureIsBoundedAndRedacted(t *testing.T) {
	secret := "navidrome-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(strings.Repeat("x", 9000) + secret))
	}))
	defer server.Close()
	navidrome := Navidrome{BaseURL: server.URL, AdminPassword: secret, HTTP: server.Client()}
	_, err := navidrome.Apply(context.Background())
	if err == nil || !strings.Contains(err.Error(), "502") || strings.Contains(err.Error(), secret) || len(err.Error()) > 8500 {
		t.Fatalf("unsafe error length=%d err=%v", len(err.Error()), err)
	}
}
