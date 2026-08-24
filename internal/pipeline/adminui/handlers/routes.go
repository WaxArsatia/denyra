package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/a-h/templ"
	"github.com/waxarsatia/denyra/internal/pipeline/adminui/assets"
	"github.com/waxarsatia/denyra/internal/pipeline/adminui/middleware"
	"github.com/waxarsatia/denyra/internal/pipeline/adminui/views"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

type Dependencies struct {
	Auth                 application.AuthService
	Reader               application.AdminReader
	Acquisition          application.AcquisitionEvidenceReader
	Reviews              application.ReviewDecisionService
	Submissions          application.SubmissionService
	Uploads              *application.UploadService
	Previews             *application.SubmissionPreviewService
	MigrationReader      application.MigrationAdminReader
	MigrationChecks      application.MigrationCheckService
	Migrations           application.MigrationService
	NotifyMigrationBatch func(string)
	Assets               *assets.Bundle
	ConfigSnapshot       string
}

type Console struct{ dependencies Dependencies }

func New(dependencies Dependencies) (http.Handler, error) {
	if dependencies.Reader == nil || dependencies.Assets == nil {
		return nil, fmt.Errorf("admin UI reader and assets are required")
	}
	console := Console{dependencies: dependencies}
	login := Login{Auth: dependencies.Auth, Assets: dependencies.Assets.Paths}
	account := Account{Auth: dependencies.Auth}
	public := http.NewServeMux()
	public.Handle("/static/", dependencies.Assets.Handler())
	public.HandleFunc("GET /login", login.Get)
	public.HandleFunc("POST /login", login.Post)
	private := http.NewServeMux()
	private.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/reviews", http.StatusSeeOther) })
	private.HandleFunc("GET /reviews", console.reviews)
	private.HandleFunc("GET /reviews/{candidateID}", console.review)
	private.HandleFunc("POST /reviews/{candidateID}/{action}", console.reviewAction)
	private.HandleFunc("GET /incoming", console.incoming)
	private.HandleFunc("GET /unmanaged", console.unmanaged)
	private.HandleFunc("POST /unmanaged/check", console.checkUnmanaged)
	private.HandleFunc("GET /migration-batches/{batchID}", console.migrationBatch)
	private.HandleFunc("POST /migration-batches/{batchID}/confirm", console.confirmMigrations)
	private.HandleFunc("POST /migration-items/{itemID}/retry", console.retryMigration)
	private.HandleFunc("GET /incoming/{submissionID}/artwork", console.incomingArtwork)
	private.HandleFunc("POST /incoming/{submissionID}/artwork", console.replaceIncomingArtwork)
	private.HandleFunc("GET /incoming/{submissionID}", console.incomingDetail)
	private.HandleFunc("POST /incoming/{submissionID}/submit", console.submit)
	private.HandleFunc("POST /upload-sessions", console.createUploadSession)
	private.HandleFunc("GET /upload-sessions/{sessionID}", console.getUploadSession)
	private.HandleFunc("PUT /upload-sessions/{sessionID}/files/{entryID}", console.putUploadFile)
	private.HandleFunc("POST /upload-sessions/{sessionID}/finalize", console.finalizeUploadSession)
	private.HandleFunc("DELETE /upload-sessions/{sessionID}", console.deleteUploadSession)
	private.HandleFunc("GET /acquisitions/{jobID}", console.acquisition)
	private.HandleFunc("GET /audit", console.audit)
	private.HandleFunc("GET /account/password", console.account)
	private.HandleFunc("POST /account/password", account.ChangePassword)
	private.HandleFunc("POST /logout", account.Logout)
	private.HandleFunc("GET /sessions", console.sessions)
	private.HandleFunc("POST /sessions/revoke-all", account.LogoutAll)
	private.HandleFunc("POST /sessions/{sessionID}/revoke", account.Revoke)
	protected := middleware.RequireAuth(dependencies.Auth, middleware.RequireCSRF(dependencies.Auth, private))
	public.Handle("/", protected)
	return securityHeaders(public), nil
}

func (c Console) shell(r *http.Request) views.Shell {
	principal, _ := middleware.Principal(r)
	csrf := ""
	if cookie, err := r.Cookie(middleware.CSRFCookie); err == nil {
		csrf = cookie.Value
	}
	return views.Shell{Deployment: "Denyra", ConfigSnapshot: c.dependencies.ConfigSnapshot, Readiness: "local ready", ReadinessClass: "ok", Username: principal.Username, CSRFToken: csrf, Assets: c.dependencies.Assets.Paths}
}

func (c Console) reviews(w http.ResponseWriter, r *http.Request) {
	items, next, err := c.dependencies.Reader.Reviews(r.Context(), 50, r.URL.Query().Get("cursor"))
	page := views.ReviewsPage{Shell: c.shell(r), Items: items, Next: next}
	if err != nil {
		page.Error = "Unable to load review queue."
	}
	c.render(w, r, views.Reviews(page), views.ReviewsContent(page))
}
func (c Console) review(w http.ResponseWriter, r *http.Request) {
	detail, err := c.dependencies.Reader.Review(r.Context(), r.PathValue("candidateID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	page := views.ReviewPage{Shell: c.shell(r), Detail: detail}
	c.render(w, r, views.ReviewDetail(page), views.ReviewDetailContent(page))
}
func (c Console) incoming(w http.ResponseWriter, r *http.Request) {
	items, next, err := c.dependencies.Reader.Submissions(r.Context(), 50, r.URL.Query().Get("cursor"))
	page := views.IncomingPage{Shell: c.shell(r), Items: items, Next: next}
	if c.dependencies.Uploads != nil {
		principal, _ := middleware.Principal(r)
		uploadSessions, uploadErr := c.dependencies.Uploads.Sessions(r.Context(), principal.UserID)
		page.UploadSessions = uploadSessions
		page.UploadConcurrency = c.dependencies.Uploads.Policy.BrowserConcurrency
		if uploadErr != nil && err == nil {
			err = uploadErr
		}
	}
	if err != nil {
		page.Error = "Unable to load incoming submissions."
	}
	c.render(w, r, views.Incoming(page), views.IncomingContent(page))
}
func (c Console) incomingDetail(w http.ResponseWriter, r *http.Request) {
	if c.dependencies.Previews == nil {
		http.NotFound(w, r)
		return
	}
	preview, err := c.dependencies.Previews.Preview(r.Context(), r.PathValue("submissionID"), false)
	if err != nil {
		http.Error(w, "preview unavailable", http.StatusConflict)
		return
	}
	page := views.IncomingDetailPage{Shell: c.shell(r), Preview: preview}
	c.render(w, r, views.IncomingDetail(page), views.IncomingDetailContent(page))
}

func (c Console) incomingArtwork(w http.ResponseWriter, r *http.Request) {
	if c.dependencies.Previews == nil {
		http.NotFound(w, r)
		return
	}
	path, err := c.dependencies.Previews.ArtworkPath(r.PathValue("submissionID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Disposition", "inline")
	http.ServeFile(w, r, path)
}

func (c Console) replaceIncomingArtwork(w http.ResponseWriter, r *http.Request) {
	if c.dependencies.Previews == nil || c.dependencies.Previews.ArtworkMaxBytes() <= 0 {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, c.dependencies.Previews.ArtworkMaxBytes()+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		http.Error(w, "invalid artwork upload", http.StatusRequestEntityTooLarge)
		return
	}
	file, _, err := r.FormFile("artwork")
	if err != nil {
		http.Error(w, "artwork file is required", http.StatusBadRequest)
		return
	}
	defer file.Close()
	if _, err := c.dependencies.Previews.ReplaceArtwork(r.Context(), r.PathValue("submissionID"), file); err != nil {
		http.Error(w, "artwork replacement failed", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/incoming/"+r.PathValue("submissionID"), http.StatusSeeOther)
}
func (c Console) audit(w http.ResponseWriter, r *http.Request) {
	items, next, err := c.dependencies.Reader.Audit(r.Context(), 50, r.URL.Query().Get("cursor"))
	page := views.AuditPage{Shell: c.shell(r), Items: items, Next: next}
	if err != nil {
		page.Error = "Unable to load audit log."
	}
	c.render(w, r, views.Audit(page), views.AuditContent(page))
}
func (c Console) sessions(w http.ResponseWriter, r *http.Request) {
	principal, _ := middleware.Principal(r)
	items, err := c.dependencies.Reader.Sessions(r.Context(), principal.UserID, principal.SessionID)
	page := views.SessionsPage{Shell: c.shell(r), Items: items}
	if err != nil {
		page.Error = "Unable to load sessions."
	}
	c.render(w, r, views.Sessions(page), views.SessionsContent(page))
}
func (c Console) account(w http.ResponseWriter, r *http.Request) {
	page := views.AccountPage{Shell: c.shell(r)}
	c.render(w, r, views.Account(page), views.AccountContent(page))
}
func (c Console) acquisition(w http.ResponseWriter, r *http.Request) {
	page := views.AcquisitionPage{Shell: c.shell(r)}
	if c.dependencies.Acquisition == nil {
		page.Error = "Gateway evidence endpoint is unavailable."
		page.Degraded = true
	} else {
		page.Evidence, page.Error = c.loadAcquisition(r)
		page.Degraded = page.Error != ""
	}
	c.render(w, r, views.Acquisition(page), views.AcquisitionContent(page))
}
func (c Console) loadAcquisition(r *http.Request) (application.AcquisitionEvidence, string) {
	item, err := c.dependencies.Acquisition.AcquisitionEvidence(r.Context(), r.PathValue("jobID"))
	if err != nil {
		return item, "Unable to load gateway evidence."
	}
	return item, ""
}

func (c Console) reviewAction(w http.ResponseWriter, r *http.Request) {
	principal, _ := middleware.Principal(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	revision, err := strconv.ParseUint(r.Form.Get("state_revision"), 10, 64)
	if err != nil {
		http.Error(w, "invalid state revision", http.StatusBadRequest)
		return
	}
	reason := strings.TrimSpace(r.Form.Get("reason"))
	if r.Form.Get("confirm") != "yes" {
		http.Error(w, "explicit confirmation required", http.StatusBadRequest)
		return
	}
	switch r.PathValue("action") {
	case "approve":
		err = c.dependencies.Reviews.Approve(r.Context(), r.PathValue("candidateID"), revision, principal.UserID, r.Form.Get("release_mbid"), reason)
	case "reject":
		err = c.dependencies.Reviews.Reject(r.Context(), r.PathValue("candidateID"), revision, principal.UserID, reason)
	case "retry":
		err = c.dependencies.Reviews.Retry(r.Context(), r.PathValue("candidateID"), revision, principal.UserID, reason)
	case "cancel":
		err = c.dependencies.Reviews.Cancel(r.Context(), r.PathValue("candidateID"), revision, principal.UserID, reason)
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		var stale *domain.StaleRevisionError
		if errors.As(err, &stale) {
			detail, loadErr := c.dependencies.Reader.Review(r.Context(), r.PathValue("candidateID"))
			if loadErr != nil {
				http.Error(w, "stale state", http.StatusConflict)
				return
			}
			w.WriteHeader(http.StatusConflict)
			_ = views.ReviewDetailContent(views.ReviewPage{Shell: c.shell(r), Detail: detail, Error: fmt.Sprintf("Stale decision: current state %s at revision %d. Review the refreshed evidence.", stale.State, stale.Current)}).Render(r.Context(), w)
			return
		}
		http.Error(w, "decision failed", http.StatusBadRequest)
		return
	}
	detail, err := c.dependencies.Reader.Review(r.Context(), r.PathValue("candidateID"))
	if err != nil {
		http.Redirect(w, r, "/reviews", http.StatusSeeOther)
		return
	}
	c.render(w, r, views.ReviewDetail(views.ReviewPage{Shell: c.shell(r), Detail: detail}), views.ReviewDetailContent(views.ReviewPage{Shell: c.shell(r), Detail: detail}))
}
func (c Console) submit(w http.ResponseWriter, r *http.Request) {
	principal, _ := middleware.Principal(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	revision, err := strconv.ParseUint(r.Form.Get("state_revision"), 10, 64)
	if r.Form.Get("state_revision") == "" {
		revision = 0
		err = nil
	}
	var decision domain.SubmissionDecision
	if err == nil {
		decision, err = c.submissionDecision(r)
	}
	if err == nil {
		err = c.dependencies.Submissions.Submit(r.Context(), r.PathValue("submissionID"), revision, principal.UserID, decision)
	}
	if err != nil {
		http.Error(w, "submission failed", http.StatusConflict)
		return
	}
	http.Redirect(w, r, "/incoming", http.StatusSeeOther)
}

func (c Console) submissionDecision(r *http.Request) (domain.SubmissionDecision, error) {
	if c.dependencies.Previews == nil {
		return domain.SubmissionDecision{}, fmt.Errorf("preview service unavailable")
	}
	preview, err := c.dependencies.Previews.Preview(r.Context(), r.PathValue("submissionID"), false)
	if err != nil {
		return domain.SubmissionDecision{}, err
	}
	paths, titles, artists := r.Form["track_path"], r.Form["track_title"], r.Form["track_artist"]
	tracks, discs := r.Form["track_number"], r.Form["disc_number"]
	if len(paths) != len(preview.Metadata.Tracks) || len(titles) != len(paths) || len(artists) != len(paths) || len(tracks) != len(paths) || len(discs) != len(paths) {
		return domain.SubmissionDecision{}, fmt.Errorf("track form is incomplete")
	}
	metadata := preview.Metadata
	metadata.AlbumArtist, metadata.Album = strings.TrimSpace(r.Form.Get("album_artist")), strings.TrimSpace(r.Form.Get("album"))
	metadata.Date, metadata.Edition = strings.TrimSpace(r.Form.Get("date")), strings.TrimSpace(r.Form.Get("edition"))
	metadata.DiscTotal = 0
	for index := range metadata.Tracks {
		if paths[index] != metadata.Tracks[index].RelativePath {
			return domain.SubmissionDecision{}, fmt.Errorf("track paths changed")
		}
		track, trackErr := strconv.Atoi(tracks[index])
		disc, discErr := strconv.Atoi(discs[index])
		if trackErr != nil || discErr != nil {
			return domain.SubmissionDecision{}, fmt.Errorf("invalid track position")
		}
		metadata.Tracks[index].Title, metadata.Tracks[index].Artist = strings.TrimSpace(titles[index]), strings.TrimSpace(artists[index])
		metadata.Tracks[index].Track, metadata.Tracks[index].Disc = track, disc
		if disc > metadata.DiscTotal {
			metadata.DiscTotal = disc
		}
	}
	metadata.TrackTotal = len(metadata.Tracks)
	return domain.SubmissionDecision{PreviewFingerprint: r.Form.Get("preview_fingerprint"), Destination: domain.Destination(r.Form.Get("destination")), ReleaseMBID: strings.TrimSpace(r.Form.Get("release_mbid")), Metadata: metadata, Artwork: preview.Artwork}, nil
}

func (c Console) render(w http.ResponseWriter, r *http.Request, full, fragment templ.Component) {
	component := full
	if r.Header.Get("HX-Request") == "true" {
		component = fragment
	}
	if err := component.Render(r.Context(), w); err != nil {
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'; object-src 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if !strings.HasPrefix(r.URL.Path, "/static/") {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		}
		next.ServeHTTP(w, r)
	})
}
