package middleware

import (
	"context"
	"net/http"

	"github.com/waxarsatia/denyra/internal/pipeline/application"
)

const SessionCookie = "denyra_session"
const CSRFCookie = "denyra_csrf"

type principalKey struct{}

func RequireAuth(auth application.AuthService, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		cookie, err := request.Cookie(SessionCookie)
		if err != nil {
			http.Redirect(writer, request, "/login", http.StatusSeeOther)
			return
		}
		principal, err := auth.Authenticate(request.Context(), cookie.Value)
		if err != nil {
			ClearCookies(writer)
			http.Redirect(writer, request, "/login", http.StatusSeeOther)
			return
		}
		ctx := context.WithValue(request.Context(), principalKey{}, principal)
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

func Principal(request *http.Request) (application.Principal, bool) {
	principal, ok := request.Context().Value(principalKey{}).(application.Principal)
	return principal, ok
}

func SetCookies(writer http.ResponseWriter, credentials application.SessionCredentials) {
	http.SetCookie(writer, &http.Cookie{Name: SessionCookie, Value: credentials.Token, Path: "/", Expires: credentials.ExpiresAt, HttpOnly: true, Secure: false, SameSite: http.SameSiteStrictMode})
	http.SetCookie(writer, &http.Cookie{Name: CSRFCookie, Value: credentials.CSRFToken, Path: "/", Expires: credentials.ExpiresAt, HttpOnly: false, Secure: false, SameSite: http.SameSiteStrictMode})
}

func ClearCookies(writer http.ResponseWriter) {
	http.SetCookie(writer, &http.Cookie{Name: SessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: false, SameSite: http.SameSiteStrictMode})
	http.SetCookie(writer, &http.Cookie{Name: CSRFCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: false, Secure: false, SameSite: http.SameSiteStrictMode})
}
