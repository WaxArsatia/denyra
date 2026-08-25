package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/adminui/assets"
	"github.com/waxarsatia/denyra/internal/pipeline/adminui/middleware"
	"github.com/waxarsatia/denyra/internal/pipeline/adminui/views"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
)

type Login struct {
	Auth     application.AuthService
	Assets   assets.Paths
	Throttle *LoginThrottle
	Now      func() time.Time
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
	username := request.Form.Get("username")
	key := loginThrottleKey(username, request.RemoteAddr)
	if retry, allowed := h.Throttle.Allow(key, h.now()); !allowed {
		h.renderThrottled(writer, request, retry)
		return
	}
	credentials, err := h.Auth.Login(request.Context(), username, request.Form.Get("password"))
	if err != nil {
		if errors.Is(err, application.ErrAuthentication) {
			now := h.now()
			if newlyBlocked := h.Throttle.Failure(key, now); newlyBlocked {
				actor := "anonymous:" + hex.EncodeToString(key[:6])
				_ = h.Auth.Repository.AppendLoginThrottleAudit(request.Context(), actor, now)
			}
			if retry, allowed := h.Throttle.Allow(key, now); !allowed {
				h.renderThrottled(writer, request, retry)
				return
			}
			h.renderFailure(writer, request)
			return
		}
		http.Error(writer, "internal error", http.StatusInternalServerError)
		return
	}
	h.Throttle.Success(key)
	middleware.SetCookies(writer, credentials)
	http.Redirect(writer, request, "/reviews", http.StatusSeeOther)
}

func (h Login) renderThrottled(writer http.ResponseWriter, request *http.Request, retry time.Duration) {
	seconds := int64((retry + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	writer.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.WriteHeader(http.StatusTooManyRequests)
	_ = views.Login(h.Assets, true).Render(request.Context(), writer)
}

func (h Login) now() time.Time {
	if h.Now != nil {
		return h.Now().UTC()
	}
	return time.Now().UTC()
}

func loginThrottleKey(username, remoteAddress string) [32]byte {
	host := remoteAddress
	if parsed, _, err := net.SplitHostPort(remoteAddress); err == nil {
		host = parsed
	}
	identity := strings.ToLower(strings.TrimSpace(username)) + "\x00" + strings.ToLower(strings.TrimSpace(host))
	return sha256.Sum256([]byte(identity))
}

func (h Login) renderFailure(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.WriteHeader(http.StatusUnauthorized)
	_ = views.Login(h.Assets, true).Render(request.Context(), writer)
}
