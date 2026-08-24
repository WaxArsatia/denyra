package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/waxarsatia/denyra/internal/pipeline/adminui/middleware"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

type uploadError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (c Console) createUploadSession(w http.ResponseWriter, r *http.Request) {
	if c.dependencies.Uploads == nil {
		writeUploadError(w, http.StatusServiceUnavailable, "UPLOAD_UNAVAILABLE", "Browser upload is unavailable.")
		return
	}
	limit := int64(c.dependencies.Uploads.Policy.MaxEntries)*8192 + 1024
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	defer r.Body.Close()
	var manifest domain.UploadManifest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		code, status := "INVALID_MANIFEST", http.StatusBadRequest
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			code, status = "UPLOAD_LIMIT", http.StatusRequestEntityTooLarge
		}
		writeUploadError(w, status, code, "The upload manifest is invalid or exceeds its configured limit.")
		return
	}
	if err := ensureJSONEnd(decoder); err != nil {
		writeUploadError(w, http.StatusBadRequest, "INVALID_MANIFEST", "The upload manifest must contain one JSON object.")
		return
	}
	principal, _ := middleware.Principal(r)
	session, err := c.dependencies.Uploads.Create(r.Context(), principal.UserID, manifest)
	if err != nil {
		code, status := "INVALID_MANIFEST", http.StatusBadRequest
		if strings.Contains(err.Error(), "exceeds") || strings.Contains(err.Error(), "limits") || strings.Contains(err.Error(), "byte policy") {
			code, status = "UPLOAD_LIMIT", http.StatusRequestEntityTooLarge
		}
		writeUploadError(w, status, code, "The upload manifest is invalid or exceeds its configured limit.")
		return
	}
	writeUploadJSON(w, http.StatusCreated, session)
}

func (c Console) putUploadFile(w http.ResponseWriter, r *http.Request) {
	if c.dependencies.Uploads == nil {
		writeUploadError(w, http.StatusServiceUnavailable, "UPLOAD_UNAVAILABLE", "Browser upload is unavailable.")
		return
	}
	principal, _ := middleware.Principal(r)
	session, err := c.dependencies.Uploads.Session(r.Context(), principal.UserID, r.PathValue("sessionID"))
	if err != nil {
		writeUploadError(w, http.StatusConflict, "SESSION_CONFLICT", "The upload session cannot accept this file.")
		return
	}
	entry, found := findUploadEntry(session.Files, r.PathValue("entryID"))
	if !found {
		writeUploadError(w, http.StatusConflict, "ENTRY_MISMATCH", "The uploaded file does not match the session manifest.")
		return
	}
	if r.ContentLength >= 0 && r.ContentLength != entry.SizeBytes {
		writeUploadError(w, http.StatusRequestEntityTooLarge, "ENTRY_MISMATCH", "The uploaded file size does not match the manifest.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, entry.SizeBytes+1)
	defer r.Body.Close()
	updated, err := c.dependencies.Uploads.PutFile(r.Context(), principal.UserID, session.ID, entry.ID, r.Body)
	if err != nil {
		if errors.Is(err, domain.ErrUploadSizeMismatch) {
			writeUploadError(w, http.StatusRequestEntityTooLarge, "ENTRY_MISMATCH", "The uploaded file size does not match the manifest.")
			return
		}
		if errors.Is(err, application.ErrUploadConflict) || errors.Is(err, application.ErrUploadForbidden) {
			writeUploadError(w, http.StatusConflict, "SESSION_CONFLICT", "The upload session changed; refresh and retry.")
			return
		}
		writeUploadError(w, http.StatusBadRequest, "ENTRY_MISMATCH", "The uploaded file could not be stored.")
		return
	}
	writeUploadJSON(w, http.StatusOK, updated)
}

func (c Console) finalizeUploadSession(w http.ResponseWriter, r *http.Request) {
	if c.dependencies.Uploads == nil {
		writeUploadError(w, http.StatusServiceUnavailable, "UPLOAD_UNAVAILABLE", "Browser upload is unavailable.")
		return
	}
	principal, _ := middleware.Principal(r)
	session, err := c.dependencies.Uploads.Finalize(r.Context(), principal.UserID, r.PathValue("sessionID"))
	if err != nil {
		writeUploadError(w, http.StatusConflict, "FINALIZE_CONFLICT", "The upload is incomplete or its files changed.")
		return
	}
	writeUploadJSON(w, http.StatusOK, session)
}

func (c Console) deleteUploadSession(w http.ResponseWriter, r *http.Request) {
	if c.dependencies.Uploads == nil {
		writeUploadError(w, http.StatusServiceUnavailable, "UPLOAD_UNAVAILABLE", "Browser upload is unavailable.")
		return
	}
	principal, _ := middleware.Principal(r)
	if err := c.dependencies.Uploads.Delete(r.Context(), principal.UserID, r.PathValue("sessionID")); err != nil {
		writeUploadError(w, http.StatusConflict, "SESSION_CONFLICT", "The upload session cannot be deleted.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c Console) getUploadSession(w http.ResponseWriter, r *http.Request) {
	if c.dependencies.Uploads == nil {
		writeUploadError(w, http.StatusServiceUnavailable, "UPLOAD_UNAVAILABLE", "Browser upload is unavailable.")
		return
	}
	principal, _ := middleware.Principal(r)
	session, err := c.dependencies.Uploads.Session(r.Context(), principal.UserID, r.PathValue("sessionID"))
	if err != nil {
		writeUploadError(w, http.StatusNotFound, "SESSION_CONFLICT", "The upload session was not found.")
		return
	}
	writeUploadJSON(w, http.StatusOK, session)
}

func findUploadEntry(files []domain.UploadFileSpec, id string) (domain.UploadFileSpec, bool) {
	for _, file := range files {
		if file.ID == id {
			return file, true
		}
	}
	return domain.UploadFileSpec{}, false
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("extra JSON value")
	}
	return err
}

func writeUploadJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeUploadError(w http.ResponseWriter, status int, code, message string) {
	writeUploadJSON(w, status, uploadError{Code: code, Message: message})
}
