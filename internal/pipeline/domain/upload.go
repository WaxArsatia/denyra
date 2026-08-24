package domain

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

var ErrUploadSizeMismatch = errors.New("upload entry size mismatch")

type UploadFileSpec struct {
	ID           string `json:"id"`
	RelativePath string `json:"relative_path"`
	MediaType    string `json:"media_type"`
	SizeBytes    int64  `json:"size_bytes"`
	Status       string `json:"status,omitempty"`
}

type UploadManifest struct {
	Files []UploadFileSpec `json:"files"`
}

type UploadLimits struct {
	MaxFileBytes    int64
	MaxSessionBytes int64
	MaxEntries      int
}

type UploadSession struct {
	ID           string           `json:"id"`
	SubmissionID string           `json:"submission_id"`
	Actor        string           `json:"actor"`
	Status       string           `json:"status"`
	Revision     uint64           `json:"revision"`
	Files        []UploadFileSpec `json:"files"`
	CreatedAt    time.Time        `json:"created_at"`
	UpdatedAt    time.Time        `json:"updated_at"`
}

const (
	UploadSessionOpen      = "OPEN"
	UploadSessionFinalized = "FINALIZED"
	UploadSessionDeleted   = "DELETED"
	UploadEntryPending     = "PENDING"
	UploadEntryComplete    = "COMPLETE"
)

func ValidateUploadManifest(manifest UploadManifest, limits UploadLimits) error {
	if limits.MaxFileBytes <= 0 || limits.MaxSessionBytes <= 0 || limits.MaxEntries <= 0 || limits.MaxSessionBytes < limits.MaxFileBytes {
		return fmt.Errorf("upload limits are invalid")
	}
	if len(manifest.Files) == 0 {
		return fmt.Errorf("upload manifest is empty")
	}
	if len(manifest.Files) > limits.MaxEntries {
		return fmt.Errorf("upload manifest exceeds %d entries", limits.MaxEntries)
	}
	seen := make(map[string]string, len(manifest.Files))
	var total int64
	for _, file := range manifest.Files {
		key, err := UploadPathKey(file.RelativePath)
		if err != nil {
			return err
		}
		if previous, exists := seen[key]; exists {
			return fmt.Errorf("upload paths %q and %q collide after normalization", previous, file.RelativePath)
		}
		seen[key] = file.RelativePath
		if file.SizeBytes < 0 || file.SizeBytes > limits.MaxFileBytes {
			return fmt.Errorf("upload file %q exceeds its byte policy", file.RelativePath)
		}
		if total > limits.MaxSessionBytes-file.SizeBytes {
			return fmt.Errorf("upload manifest exceeds its session byte policy")
		}
		total += file.SizeBytes
	}
	return nil
}

func UploadPathKey(value string) (string, error) {
	if value == "" || !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') || strings.ContainsRune(value, '\\') || strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("unsafe upload path %q", value)
	}
	segments := strings.Split(value, "/")
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("unsafe upload path %q", value)
		}
	}
	if cleaned := path.Clean(value); cleaned != value || cleaned == "." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("unsafe upload path %q", value)
	}
	return cases.Fold().String(norm.NFC.String(value)), nil
}
