package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

var ErrEnrichmentNotFound = errors.New("enrichment not found")

type LyricsProvider interface {
	Get(context.Context, domain.LyricsQuery) (domain.LyricsResult, domain.ProviderEvidence, error)
}

type ArtworkProvider interface {
	Fetch(context.Context, string) ([]byte, domain.ProviderEvidence, error)
}

type EnrichmentTrack struct {
	RelativeFLAC string
	Query        domain.LyricsQuery
}

type EnrichmentItem struct {
	Kind           string                  `json:"kind"`
	RelativePath   string                  `json:"relative_path,omitempty"`
	Classification string                  `json:"classification"`
	SHA256         string                  `json:"sha256,omitempty"`
	Evidence       domain.ProviderEvidence `json:"evidence"`
}

type EnrichmentResult struct {
	Items    []EnrichmentItem `json:"items"`
	Warnings []domain.Warning `json:"warnings"`
}

type EnrichmentService struct {
	WorkRoot     string
	EvidenceRoot string
	Lyrics       LyricsProvider
	Artwork      ArtworkProvider
}

func (s EnrichmentService) Enrich(ctx context.Context, candidateID, releaseMBID string, tracks []EnrichmentTrack) (EnrichmentResult, error) {
	result := EnrichmentResult{}
	if err := domain.ValidateCandidateID(candidateID); err != nil {
		return result, err
	}
	if s.Lyrics == nil {
		return result, fmt.Errorf("lyrics provider is not configured")
	}
	workPath := filepath.Join(s.WorkRoot, candidateID)
	for _, track := range tracks {
		if filepath.IsAbs(track.RelativeFLAC) || filepath.Clean(track.RelativeFLAC) != track.RelativeFLAC || strings.ToLower(filepath.Ext(track.RelativeFLAC)) != ".flac" {
			return result, fmt.Errorf("invalid enrichment track path %q", track.RelativeFLAC)
		}
		lyrics, evidence, err := s.Lyrics.Get(ctx, track.Query)
		item := EnrichmentItem{Kind: "LYRICS", RelativePath: strings.TrimSuffix(track.RelativeFLAC, filepath.Ext(track.RelativeFLAC)) + ".lrc", Evidence: evidence}
		if err != nil {
			item.Classification = "UNAVAILABLE"
			result.Items = append(result.Items, item)
			result.Warnings = append(result.Warnings, domain.Warning{Kind: domain.WarningNonBlocking, Code: "LYRICS_UNAVAILABLE", Details: err.Error()})
			continue
		}
		content, classification := selectLyrics(lyrics)
		item.Classification = classification
		if content == "" {
			result.Items = append(result.Items, item)
			if !lyrics.Instrumental {
				result.Warnings = append(result.Warnings, domain.Warning{Kind: domain.WarningNonBlocking, Code: "LYRICS_NOT_FOUND", Details: "provider returned no usable lyrics"})
			}
			continue
		}
		target := filepath.Join(workPath, filepath.FromSlash(item.RelativePath))
		if err := writeAtomic(target, []byte(content)); err != nil {
			return result, err
		}
		hash := sha256.Sum256([]byte(content))
		item.SHA256 = hex.EncodeToString(hash[:])
		result.Items = append(result.Items, item)
	}
	if s.Artwork != nil {
		bytes, evidence, err := s.Artwork.Fetch(ctx, releaseMBID)
		item := EnrichmentItem{Kind: "ARTWORK", Evidence: evidence}
		if err != nil || len(bytes) == 0 {
			item.Classification = "UNAVAILABLE"
			result.Items = append(result.Items, item)
			details := "provider returned no artwork"
			if err != nil {
				details = err.Error()
			}
			result.Warnings = append(result.Warnings, domain.Warning{Kind: domain.WarningNonBlocking, Code: "ARTWORK_UNAVAILABLE", Details: details})
		} else {
			hash := sha256.Sum256(bytes)
			item.SHA256, item.Classification = hex.EncodeToString(hash[:]), "EVIDENCE_STORED"
			target := filepath.Join(s.EvidenceRoot, candidateID, "artwork-"+item.SHA256)
			if err := writeAtomic(target, bytes); err != nil {
				return result, err
			}
			result.Items = append(result.Items, item)
		}
	}
	return result, nil
}

func selectLyrics(result domain.LyricsResult) (string, string) {
	if strings.TrimSpace(result.WordSynced) != "" {
		return strings.TrimSpace(result.WordSynced) + "\n", "WORD_SYNCED"
	}
	if strings.TrimSpace(result.Synced) != "" {
		return strings.TrimSpace(result.Synced) + "\n", "LINE_SYNCED"
	}
	if strings.TrimSpace(result.Plain) != "" {
		return strings.TrimSpace(result.Plain) + "\n", "PLAIN"
	}
	if result.Instrumental {
		return "", "INSTRUMENTAL"
	}
	return "", "NO_RESULT"
}

func writeAtomic(target string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".denyra-enrichment-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(name, target)
}
