package handlers

import (
	"errors"
	"html/template"
	"net/http"

	"github.com/waxarsatia/denyra/internal/pipeline/adminui/middleware"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
)

type Login struct{ Auth application.AuthService }

var loginTemplate = template.Must(template.New("login").Parse(`<!doctype html><html><body><main><h1>Denyra admin</h1>{{if .}}<p role="alert">Authentication failed</p>{{end}}<form method="post" action="/login"><label>Username<input name="username" autocomplete="username" required></label><label>Password<input type="password" name="password" autocomplete="current-password" required></label><button type="submit">Sign in</button></form></main></body></html>`))

func (h Login) Get(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = loginTemplate.Execute(writer, false)
}

func (h Login) Post(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if err := request.ParseForm(); err != nil {
		h.renderFailure(writer)
		return
	}
	credentials, err := h.Auth.Login(request.Context(), request.Form.Get("username"), request.Form.Get("password"))
	if err != nil {
		if errors.Is(err, application.ErrAuthentication) {
			h.renderFailure(writer)
			return
		}
		http.Error(writer, "internal error", http.StatusInternalServerError)
		return
	}
	middleware.SetCookies(writer, credentials)
	http.Redirect(writer, request, "/reviews", http.StatusSeeOther)
}

func (h Login) renderFailure(writer http.ResponseWriter) {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.WriteHeader(http.StatusUnauthorized)
	_ = loginTemplate.Execute(writer, true)
}
