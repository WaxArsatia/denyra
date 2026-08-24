package domain

import (
	"fmt"
	"sort"
	"strings"
)

type Destination string

const (
	DestinationManaged   Destination = "MANAGED"
	DestinationUnmanaged Destination = "UNMANAGED"
)

type TrackMetadata struct {
	RelativePath string   `json:"relative_path"`
	Title        string   `json:"title"`
	Artist       string   `json:"artist"`
	Track        int      `json:"track"`
	Disc         int      `json:"disc"`
	DurationMS   int64    `json:"duration_ms"`
	ISRCs        []string `json:"isrcs,omitempty"`
}

type MetadataPlan struct {
	AlbumArtist string                         `json:"album_artist"`
	Album       string                         `json:"album"`
	Date        string                         `json:"date,omitempty"`
	Edition     string                         `json:"edition,omitempty"`
	Tracks      []TrackMetadata                `json:"tracks"`
	DiscTotal   int                            `json:"disc_total"`
	TrackTotal  int                            `json:"track_total"`
	Preserved   map[string]map[string][]string `json:"preserved,omitempty"`
}

type MetadataConflict struct {
	Field        string   `json:"field"`
	RelativePath string   `json:"relative_path,omitempty"`
	Values       []string `json:"values,omitempty"`
}

type ArtworkSelection struct {
	Source    string `json:"source,omitempty"`
	Path      string `json:"path,omitempty"`
	URL       string `json:"url,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	MediaType string `json:"media_type,omitempty"`
}

type SubmissionDecision struct {
	PreviewFingerprint string           `json:"preview_fingerprint"`
	Destination        Destination      `json:"destination"`
	ReleaseMBID        string           `json:"release_mbid,omitempty"`
	Metadata           MetadataPlan     `json:"metadata"`
	Artwork            ArtworkSelection `json:"artwork,omitempty"`
}

type SubmissionPreview struct {
	SubmissionID string              `json:"submission_id"`
	Ingress      string              `json:"ingress"`
	Revision     uint64              `json:"revision"`
	Fingerprint  string              `json:"fingerprint"`
	Metadata     MetadataPlan        `json:"metadata"`
	Conflicts    []MetadataConflict  `json:"conflicts,omitempty"`
	Identity     *IdentityPreview    `json:"identity,omitempty"`
	Draft        *SubmissionDecision `json:"draft,omitempty"`
}

type IdentityPreview struct {
	Status               string                     `json:"status"`
	SuggestedDestination Destination                `json:"suggested_destination,omitempty"`
	ExactReleaseMBID     string                     `json:"exact_release_mbid,omitempty"`
	Candidates           []IdentityCandidatePreview `json:"candidates,omitempty"`
	Evidence             []IdentityEvidence         `json:"evidence,omitempty"`
	Reason               string                     `json:"reason,omitempty"`
}

type IdentityCandidatePreview struct {
	ReleaseMBID string         `json:"release_mbid"`
	Title       string         `json:"title"`
	Artist      string         `json:"artist"`
	Date        string         `json:"date,omitempty"`
	MatchStatus DurationStatus `json:"match_status"`
}

type IdentityEvidence struct {
	Endpoint       string `json:"endpoint"`
	StatusCode     int    `json:"status_code"`
	ResponseSHA256 string `json:"response_sha256"`
	ResponseBody   []byte `json:"-"`
}

func ValidateMetadataPlan(plan MetadataPlan) error {
	if strings.TrimSpace(plan.AlbumArtist) == "" || strings.TrimSpace(plan.Album) == "" {
		return fmt.Errorf("album artist and album are required")
	}
	if len(plan.Tracks) == 0 || plan.TrackTotal != len(plan.Tracks) || plan.DiscTotal <= 0 {
		return fmt.Errorf("track and disc totals are inconsistent")
	}
	positions := make(map[int][]int)
	paths := make(map[string]bool, len(plan.Tracks))
	for _, track := range plan.Tracks {
		if strings.TrimSpace(track.RelativePath) == "" || strings.TrimSpace(track.Title) == "" || strings.TrimSpace(track.Artist) == "" || track.Track <= 0 || track.Disc <= 0 || track.Disc > plan.DiscTotal || track.DurationMS <= 0 {
			return fmt.Errorf("track metadata is incomplete at %q", track.RelativePath)
		}
		if paths[track.RelativePath] {
			return fmt.Errorf("duplicate track path %q", track.RelativePath)
		}
		paths[track.RelativePath] = true
		positions[track.Disc] = append(positions[track.Disc], track.Track)
	}
	for disc := 1; disc <= plan.DiscTotal; disc++ {
		tracks := positions[disc]
		if len(tracks) == 0 {
			return fmt.Errorf("disc %d has no tracks", disc)
		}
		sort.Ints(tracks)
		for index, position := range tracks {
			if position != index+1 {
				return fmt.Errorf("disc %d track positions are not unique and contiguous", disc)
			}
		}
	}
	return nil
}

func ValidateSubmissionDecision(decision SubmissionDecision) error {
	if strings.TrimSpace(decision.PreviewFingerprint) == "" {
		return fmt.Errorf("preview fingerprint is required")
	}
	if err := ValidateMetadataPlan(decision.Metadata); err != nil {
		return err
	}
	switch decision.Destination {
	case DestinationUnmanaged:
		if decision.ReleaseMBID != "" {
			return fmt.Errorf("unmanaged decision cannot carry a release MBID")
		}
	case DestinationManaged:
		if _, err := CanonicalMBID(decision.ReleaseMBID); err != nil {
			return fmt.Errorf("managed decision release MBID: %w", err)
		}
	default:
		return fmt.Errorf("unknown submission destination %q", decision.Destination)
	}
	return nil
}
