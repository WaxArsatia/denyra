package httpx_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waxarsatia/denyra/internal/platform/httpx"
)

func TestRequestIDPropagatesValidIDAndGeneratesMissingID(t *testing.T) {
	t.Parallel()
	handler := httpx.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, httpx.RequestIDFromContext(r.Context()))
	}))
	for name, supplied := range map[string]string{"propagate": "req_abc-123", "generate": ""} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if supplied != "" {
				req.Header.Set("X-Request-ID", supplied)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			got := response.Body.String()
			if got == "" || response.Header().Get("X-Request-ID") != got {
				t.Fatalf("request ID not propagated: body=%q header=%q", got, response.Header().Get("X-Request-ID"))
			}
			if supplied != "" && got != supplied {
				t.Fatalf("request ID = %q, want %q", got, supplied)
			}
		})
	}
}

func TestBearerAuthUsesGenericUnauthorizedResponse(t *testing.T) {
	t.Parallel()
	called := false
	handler := httpx.BearerAuth([]byte("correct-secret"), http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	for _, header := range []string{"", "Bearer wrong-secret", "Basic abc"} {
		called = false
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set("Authorization", header)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if called || response.Code != http.StatusUnauthorized {
			t.Fatalf("invalid auth reached handler or status=%d", response.Code)
		}
		var body httpx.ErrorResponse
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode error: %v", err)
		}
		if body.Code != "UNAUTHORIZED" || body.Message != "authentication failed" {
			t.Fatalf("non-generic auth response: %+v", body)
		}
	}
}

func TestBearerAuthAcceptsExactToken(t *testing.T) {
	t.Parallel()
	handler := httpx.BearerAuth([]byte("correct-secret"), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer correct-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", response.Code)
	}
}

func TestLimitBodyRejectsOneByteBeyondLimit(t *testing.T) {
	t.Parallel()
	handler := httpx.LimitBody(4, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err != nil {
			httpx.WriteError(w, r, http.StatusRequestEntityTooLarge, "BODY_TOO_LARGE", "request body too large")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	for body, want := range map[string]int{"1234": http.StatusNoContent, "12345": http.StatusRequestEntityTooLarge} {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != want {
			t.Fatalf("body length %d status = %d, want %d", len(body), response.Code, want)
		}
	}
}

func TestRequireJSONRejectsWrongContentType(t *testing.T) {
	t.Parallel()
	handler := httpx.RequireJSON(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "text/plain")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", response.Code)
	}
}
