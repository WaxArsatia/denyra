package middleware

import (
	"crypto/subtle"
	"net/http"

	"github.com/waxarsatia/denyra/internal/pipeline/application"
)

func RequireCSRF(auth application.AuthService, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet || request.Method == http.MethodHead || request.Method == http.MethodOptions {
			next.ServeHTTP(writer, request)
			return
		}
		principal, ok := Principal(request)
		if !ok {
			http.Error(writer, "authentication failed", http.StatusUnauthorized)
			return
		}
		cookie, err := request.Cookie(CSRFCookie)
		if err != nil {
			http.Error(writer, "CSRF validation failed", http.StatusForbidden)
			return
		}
		submitted := request.Header.Get("X-CSRF-Token")
		if submitted == "" {
			if err := request.ParseForm(); err == nil {
				submitted = request.Form.Get("_csrf")
			}
		}
		cookieMatches := subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(submitted)) == 1
		if !cookieMatches || !auth.ValidateCSRF(principal, submitted) {
			http.Error(writer, "CSRF validation failed", http.StatusForbidden)
			return
		}
		next.ServeHTTP(writer, request)
	})
}
