package handlers

import (
	"errors"
	"net/http"

	"github.com/waxarsatia/denyra/internal/pipeline/adminui/middleware"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
)

type Account struct{ Auth application.AuthService }

func (h Account) ChangePassword(writer http.ResponseWriter, request *http.Request) {
	principal, ok := middleware.Principal(request)
	if !ok {
		http.Error(writer, "authentication failed", http.StatusUnauthorized)
		return
	}
	if err := request.ParseForm(); err != nil {
		http.Error(writer, "invalid form", http.StatusBadRequest)
		return
	}
	credentials, err := h.Auth.ChangePassword(request.Context(), principal, request.Form.Get("current_password"), request.Form.Get("new_password"))
	if err != nil {
		if errors.Is(err, application.ErrAuthentication) {
			http.Error(writer, "authentication failed", http.StatusUnauthorized)
			return
		}
		http.Error(writer, "password change failed", http.StatusBadRequest)
		return
	}
	middleware.SetCookies(writer, credentials)
	http.Redirect(writer, request, "/account/password?changed=1", http.StatusSeeOther)
}

func (h Account) Logout(writer http.ResponseWriter, request *http.Request) {
	principal, ok := middleware.Principal(request)
	if ok {
		if err := h.Auth.Logout(request.Context(), principal); err != nil {
			http.Error(writer, "internal error", http.StatusInternalServerError)
			return
		}
	}
	middleware.ClearCookies(writer)
	http.Redirect(writer, request, "/login", http.StatusSeeOther)
}

func (h Account) LogoutAll(writer http.ResponseWriter, request *http.Request) {
	principal, ok := middleware.Principal(request)
	if !ok {
		http.Error(writer, "authentication failed", http.StatusUnauthorized)
		return
	}
	if err := h.Auth.LogoutAll(request.Context(), principal); err != nil {
		http.Error(writer, "internal error", http.StatusInternalServerError)
		return
	}
	middleware.ClearCookies(writer)
	http.Redirect(writer, request, "/login", http.StatusSeeOther)
}

func (h Account) Revoke(writer http.ResponseWriter, request *http.Request) {
	principal, ok := middleware.Principal(request)
	if !ok {
		http.Error(writer, "authentication failed", http.StatusUnauthorized)
		return
	}
	if err := h.Auth.Revoke(request.Context(), principal, request.PathValue("sessionID")); err != nil {
		http.Error(writer, "session revocation failed", http.StatusBadRequest)
		return
	}
	if request.PathValue("sessionID") == principal.SessionID {
		middleware.ClearCookies(writer)
		http.Redirect(writer, request, "/login", http.StatusSeeOther)
		return
	}
	http.Redirect(writer, request, "/sessions", http.StatusSeeOther)
}
