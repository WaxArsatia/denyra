package httpx

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"mime"
	"net/http"
	"regexp"
	"strings"

	"github.com/waxarsatia/denyra/internal/platform/ids"
)

type requestIDKey struct{}

var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if !validRequestID.MatchString(requestID) {
			generated, err := ids.NewToken(18)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			requestID = generated
		}
		w.Header().Set("X-Request-ID", requestID)
		ctx := context.WithValue(r.Context(), requestIDKey{}, requestID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func RequestIDFromContext(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDKey{}).(string)
	return requestID
}

func BearerAuth(expected []byte, next http.Handler) http.Handler {
	expectedHash := sha256.Sum256(expected)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme, token, found := strings.Cut(r.Header.Get("Authorization"), " ")
		providedHash := sha256.Sum256([]byte(token))
		valid := found && scheme == "Bearer" && subtle.ConstantTimeCompare(expectedHash[:], providedHash[:]) == 1
		if !valid {
			WriteError(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "authentication failed")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func LimitBody(limit int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		next.ServeHTTP(w, r)
	})
}

func RequireJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			WriteError(w, r, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "content type must be application/json")
			return
		}
		next.ServeHTTP(w, r)
	})
}
