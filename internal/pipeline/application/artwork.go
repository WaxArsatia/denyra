package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

var ErrArtworkNotFound = errors.New("artwork not found")

type LocalArtwork interface {
	Embedded(context.Context, string) ([]byte, string, error)
	Sidecar(string) ([]byte, string, error)
}

type URLArtworkLookup interface {
	FetchURL(context.Context, string) ([]byte, domain.ProviderEvidence, error)
}

type ReleaseArtworkLookup interface {
	FetchRelease(context.Context, string) ([]byte, domain.ProviderEvidence, error)
}

type ArtworkService struct {
	Local     LocalArtwork
	Spotify   URLArtworkLookup
	CoverArt  ReleaseArtworkLookup
	Root      string
	MaxBytes  int64
	MaxPixels int64
}

func (s ArtworkService) Select(ctx context.Context, submissionID, releaseRoot string, tags map[string]map[string][]string, identity IdentityDecision) (domain.ArtworkSelection, []domain.ProviderEvidence, error) {
	if err := s.validate(submissionID); err != nil {
		return domain.ArtworkSelection{}, nil, err
	}
	if selection, found := s.adminSelection(submissionID); found {
		return selection, nil, nil
	}
	type localCandidate struct {
		source domain.ArtworkSource
		load   func() ([]byte, string, error)
	}
	if s.Local != nil {
		for _, candidate := range []localCandidate{
			{source: domain.ArtworkEmbedded, load: func() ([]byte, string, error) { return s.Local.Embedded(ctx, releaseRoot) }},
			{source: domain.ArtworkSidecar, load: func() ([]byte, string, error) { return s.Local.Sidecar(releaseRoot) }},
		} {
			body, _, err := candidate.load()
			if err == nil {
				selection, persistErr := s.persist(submissionID, candidate.source, "", body)
				if persistErr == nil {
					return selection, nil, nil
				}
			} else if ctx.Err() != nil {
				return domain.ArtworkSelection{}, nil, ctx.Err()
			}
		}
	}

	evidence := make([]domain.ProviderEvidence, 0, 2)
	if spotifyURL := explicitSpotifyURL(tags); spotifyURL != "" && s.Spotify != nil {
		body, item, err := s.Spotify.FetchURL(ctx, spotifyURL)
		evidence = append(evidence, item)
		if err == nil {
			selection, persistErr := s.persist(submissionID, domain.ArtworkSpotifyExplicit, spotifyURL, body)
			if persistErr == nil {
				selection.SourceURL = spotifyURL
				return selection, evidence, nil
			}
		} else if ctx.Err() != nil {
			return domain.ArtworkSelection{}, evidence, ctx.Err()
		}
	}
	if s.CoverArt != nil && identityHasExactIdentifier(tags, identity) {
		body, item, err := s.CoverArt.FetchRelease(ctx, identity.Exact.Release.ReleaseMBID)
		evidence = append(evidence, item)
		if err == nil {
			selection, persistErr := s.persist(submissionID, domain.ArtworkIdentifierExact, item.Endpoint, body)
			if persistErr == nil {
				selection.SourceURL = item.Endpoint
				return selection, evidence, nil
			}
		} else if ctx.Err() != nil {
			return domain.ArtworkSelection{}, evidence, ctx.Err()
		}
	}
	return domain.ArtworkSelection{}, evidence, nil
}

func (s ArtworkService) Replace(ctx context.Context, submissionID string, body io.Reader) (domain.ArtworkSelection, error) {
	if err := s.validate(submissionID); err != nil {
		return domain.ArtworkSelection{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.ArtworkSelection{}, err
	}
	content, err := readArtwork(body, s.MaxBytes)
	if err != nil {
		return domain.ArtworkSelection{}, err
	}
	selection, err := s.persist(submissionID, domain.ArtworkAdminUpload, "admin upload", content)
	if err != nil {
		return domain.ArtworkSelection{}, err
	}
	marker := filepath.Join(s.Root, submissionID, ".admin")
	if err := os.WriteFile(marker, []byte(selection.SHA256+"\n"), 0o640); err != nil {
		return domain.ArtworkSelection{}, err
	}
	return selection, nil
}

func (s ArtworkService) Path(submissionID string) (string, error) {
	if err := s.validate(submissionID); err != nil {
		return "", err
	}
	return filepath.Join(s.Root, submissionID, "cover.jpg"), nil
}

func (s ArtworkService) persist(submissionID string, source domain.ArtworkSource, sourceURL string, body []byte) (domain.ArtworkSelection, error) {
	encoded, width, height, err := normalizeArtwork(body, s.MaxBytes, s.MaxPixels)
	if err != nil {
		return domain.ArtworkSelection{}, err
	}
	directory := filepath.Join(s.Root, submissionID)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return domain.ArtworkSelection{}, err
	}
	temporary, err := os.CreateTemp(directory, ".cover-*.jpg")
	if err != nil {
		return domain.ArtworkSelection{}, err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o640); err != nil {
		temporary.Close()
		return domain.ArtworkSelection{}, err
	}
	if _, err := temporary.Write(encoded); err != nil {
		temporary.Close()
		return domain.ArtworkSelection{}, err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return domain.ArtworkSelection{}, err
	}
	if err := temporary.Close(); err != nil {
		return domain.ArtworkSelection{}, err
	}
	target := filepath.Join(directory, "cover.jpg")
	if err := os.Rename(temporaryPath, target); err != nil {
		return domain.ArtworkSelection{}, err
	}
	hash := sha256.Sum256(encoded)
	return domain.ArtworkSelection{Source: source, Path: target, MIME: "image/jpeg", SHA256: hex.EncodeToString(hash[:]), SourceURL: sourceURL, Width: width, Height: height}, nil
}

func (s ArtworkService) validate(submissionID string) error {
	if strings.TrimSpace(s.Root) == "" || s.MaxBytes <= 0 || s.MaxPixels <= 0 {
		return fmt.Errorf("artwork service bounds and root are required")
	}
	if submissionID == "" || filepath.Base(submissionID) != submissionID || submissionID == "." || submissionID == ".." {
		return fmt.Errorf("invalid submission ID")
	}
	return nil
}

func (s ArtworkService) adminSelection(submissionID string) (domain.ArtworkSelection, bool) {
	marker := filepath.Join(s.Root, submissionID, ".admin")
	if _, err := os.Stat(marker); err != nil {
		return domain.ArtworkSelection{}, false
	}
	target := filepath.Join(s.Root, submissionID, "cover.jpg")
	body, err := os.ReadFile(target)
	if err != nil {
		return domain.ArtworkSelection{}, false
	}
	encoded, width, height, err := normalizeArtwork(body, s.MaxBytes, s.MaxPixels)
	if err != nil {
		return domain.ArtworkSelection{}, false
	}
	hash := sha256.Sum256(encoded)
	return domain.ArtworkSelection{Source: domain.ArtworkAdminUpload, Path: target, MIME: "image/jpeg", SHA256: hex.EncodeToString(hash[:]), Width: width, Height: height}, true
}

func readArtwork(reader io.Reader, maxBytes int64) ([]byte, error) {
	if reader == nil || maxBytes <= 0 {
		return nil, fmt.Errorf("artwork body and maximum size are required")
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("artwork exceeds %d bytes", maxBytes)
	}
	return body, nil
}

func normalizeArtwork(body []byte, maxBytes, maxPixels int64) ([]byte, int, int, error) {
	if int64(len(body)) > maxBytes {
		return nil, 0, 0, fmt.Errorf("artwork exceeds %d bytes", maxBytes)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("decode artwork header: %w", err)
	}
	if format != "jpeg" && format != "png" {
		return nil, 0, 0, fmt.Errorf("artwork must be JPEG or PNG")
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width) > maxPixels/int64(config.Height) {
		return nil, 0, 0, fmt.Errorf("artwork exceeds %d pixels", maxPixels)
	}
	decoded, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("decode artwork: %w", err)
	}
	canvas := image.NewRGBA(image.Rect(0, 0, config.Width, config.Height))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(canvas, canvas.Bounds(), decoded, decoded.Bounds().Min, draw.Over)
	var output bytes.Buffer
	if err := jpeg.Encode(&output, canvas, &jpeg.Options{Quality: 92}); err != nil {
		return nil, 0, 0, err
	}
	return output.Bytes(), config.Width, config.Height, nil
}

func explicitSpotifyURL(tags map[string]map[string][]string) string {
	for _, fields := range tags {
		for key, values := range fields {
			if !strings.EqualFold(key, "SPOTIFY_URL") && !strings.EqualFold(key, "SOURCE_URL") && !strings.EqualFold(key, "URL") {
				continue
			}
			for _, value := range values {
				parsed, err := url.Parse(strings.TrimSpace(value))
				if err == nil && parsed.Scheme == "https" && strings.EqualFold(parsed.Hostname(), "open.spotify.com") && parsed.RawQuery == "" && parsed.Fragment == "" {
					parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
					if len(parts) == 2 && parts[0] == "track" && parts[1] != "" {
						return parsed.String()
					}
				}
			}
		}
	}
	return ""
}

func identityHasExactIdentifier(tags map[string]map[string][]string, identity IdentityDecision) bool {
	if identity.Status != IdentityExact || identity.Exact == nil {
		return false
	}
	localISRCs := make([]string, 0)
	hasBarcode := false
	for _, fields := range tags {
		for key, values := range fields {
			switch {
			case strings.EqualFold(key, "ISRC"):
				localISRCs = append(localISRCs, values...)
			case strings.EqualFold(key, "BARCODE"), strings.EqualFold(key, "UPC"):
				hasBarcode = hasBarcode || len(values) > 0
			}
		}
	}
	for _, track := range identity.Exact.Release.Tracks {
		if intersectsFolded(localISRCs, track.ISRCs) {
			return true
		}
	}
	if hasBarcode {
		for _, evidence := range identity.Evidence {
			endpoint := strings.ToLower(evidence.Endpoint)
			if strings.Contains(endpoint, "barcode%3a") || strings.Contains(endpoint, "barcode:") {
				return true
			}
		}
	}
	return false
}
