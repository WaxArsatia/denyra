package handlers

import (
	"errors"
	"net/http"

	"github.com/waxarsatia/denyra/internal/pipeline/adminui/assets"
	"github.com/waxarsatia/denyra/internal/pipeline/adminui/middleware"
	"github.com/waxarsatia/denyra/internal/pipeline/adminui/views"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
)

type Login struct {
	Auth   application.AuthService
	Assets assets.Paths
}

func (h Login) Get(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = views.Login(h.Assets, false).Render(request.Context(), writer)
}

func (h Login) Post(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if err := request.ParseForm(); err != nil {
		h.renderFailure(writer, request)
		return
	}
	credentials, err := h.Auth.Login(request.Context(), request.Form.Get("username"), request.Form.Get("password"))
	if err != nil {
		if errors.Is(err, application.ErrAuthentication) {
			h.renderFailure(writer, request)
			return
		}
		http.Error(writer, "internal error", http.StatusInternalServerError)
		return
	}
	middleware.SetCookies(writer, credentials)
	http.Redirect(writer, request, "/reviews", http.StatusSeeOther)
}

func (h Login) renderFailure(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.WriteHeader(http.StatusUnauthorized)
	_ = views.Login(h.Assets, true).Render(request.Context(), writer)
}
