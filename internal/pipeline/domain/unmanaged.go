package domain

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
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

type ArtworkSource string

const (
	ArtworkEmbedded        ArtworkSource = "EMBEDDED"
	ArtworkSidecar         ArtworkSource = "SIDECAR"
	ArtworkSpotifyExplicit ArtworkSource = "SPOTIFY_EXPLICIT"
	ArtworkIdentifierExact ArtworkSource = "IDENTIFIER_EXACT"
	ArtworkAdminUpload     ArtworkSource = "ADMIN_UPLOAD"
)

type ArtworkSelection struct {
	Source    ArtworkSource `json:"source,omitempty"`
	Path      string        `json:"path,omitempty"`
	MIME      string        `json:"mime,omitempty"`
	SHA256    string        `json:"sha256,omitempty"`
	SourceURL string        `json:"source_url,omitempty"`
	Width     int           `json:"width,omitempty"`
	Height    int           `json:"height,omitempty"`
}

type SubmissionDecision struct {
	PreviewFingerprint string           `json:"preview_fingerprint"`
	Destination        Destination      `json:"destination"`
	ReleaseMBID        string           `json:"release_mbid,omitempty"`
	Metadata           MetadataPlan     `json:"metadata"`
	Artwork            ArtworkSelection `json:"artwork,omitempty"`
}

type PlannedFile struct {
	SourceRelative string `json:"source_relative"`
	TargetRelative string `json:"target_relative"`
	Kind           string `json:"kind"`
}

type UnmanagedPlan struct {
	CandidateID  string            `json:"candidate_id"`
	Metadata     MetadataPlan      `json:"metadata"`
	Artwork      ArtworkSelection  `json:"artwork,omitempty"`
	RelativeRoot string            `json:"relative_root"`
	Files        []PlannedFile     `json:"files"`
	Tags         map[string]TagSet `json:"tags"`
}

func BuildUnmanagedLayout(plan MetadataPlan) (string, []PlannedFile, error) {
	if err := ValidateMetadataPlan(plan); err != nil {
		return "", nil, err
	}
	artist, err := SanitizeMusicComponent(plan.AlbumArtist)
	if err != nil {
		return "", nil, fmt.Errorf("album artist path: %w", err)
	}
	album, err := SanitizeMusicComponent(plan.Album)
	if err != nil {
		return "", nil, fmt.Errorf("album path: %w", err)
	}
	if len(plan.Date) >= 4 {
		album += " (" + plan.Date[:4] + ")"
	}
	if strings.TrimSpace(plan.Edition) != "" {
		edition, err := SanitizeMusicComponent(plan.Edition)
		if err != nil {
			return "", nil, fmt.Errorf("edition path: %w", err)
		}
		album += " [" + edition + "]"
	}
	width := len(strconv.Itoa(plan.TrackTotal))
	if width < 2 {
		width = 2
	}
	files := make([]PlannedFile, 0, len(plan.Tracks))
	seen := make(map[string]string, len(plan.Tracks))
	for _, track := range plan.Tracks {
		if filepath.IsAbs(track.RelativePath) || filepath.Clean(track.RelativePath) != track.RelativePath || strings.HasPrefix(track.RelativePath, ".."+string(filepath.Separator)) {
			return "", nil, fmt.Errorf("unsafe source path %q", track.RelativePath)
		}
		title, err := SanitizeMusicComponent(track.Title)
		if err != nil {
			return "", nil, fmt.Errorf("track title path: %w", err)
		}
		target := fmt.Sprintf("%0*d - %s.flac", width, track.Track, title)
		if plan.DiscTotal > 1 {
			target = filepath.Join(fmt.Sprintf("Disc %02d", track.Disc), target)
		}
		key := cases.Fold().String(norm.NFC.String(filepath.ToSlash(target)))
		if previous, duplicate := seen[key]; duplicate {
			return "", nil, fmt.Errorf("target collision between %q and %q", previous, track.RelativePath)
		}
		seen[key] = track.RelativePath
		files = append(files, PlannedFile{SourceRelative: track.RelativePath, TargetRelative: target, Kind: "flac"})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].TargetRelative < files[j].TargetRelative })
	return filepath.Join(artist, album), files, nil
}

func SanitizeMusicComponent(value string) (string, error) {
	value = norm.NFC.String(value)
	var output strings.Builder
	for _, character := range value {
		if character == '/' || character == '\\' || unicode.IsControl(character) {
			output.WriteRune('_')
			continue
		}
		output.WriteRune(character)
	}
	cleaned := strings.TrimRight(strings.TrimSpace(output.String()), ". ")
	if cleaned == "" || cleaned == "." || cleaned == ".." {
		return "", fmt.Errorf("music path component is empty or reserved")
	}
	return cleaned, nil
}

type SubmissionPreview struct {
	SubmissionID    string              `json:"submission_id"`
	Ingress         string              `json:"ingress"`
	Revision        uint64              `json:"revision"`
	Fingerprint     string              `json:"fingerprint"`
	Metadata        MetadataPlan        `json:"metadata"`
	Conflicts       []MetadataConflict  `json:"conflicts,omitempty"`
	Identity        *IdentityPreview    `json:"identity,omitempty"`
	Artwork         ArtworkSelection    `json:"artwork,omitempty"`
	ArtworkEvidence []ProviderEvidence  `json:"artwork_evidence,omitempty"`
	Draft           *SubmissionDecision `json:"draft,omitempty"`
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
