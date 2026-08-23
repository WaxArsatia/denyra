package domain

import (
	"encoding/hex"
	"fmt"
	"math"
	"strings"
)

type ArtistCredit struct {
	Name       string `json:"name"`
	ArtistMBID string `json:"artist_mbid"`
	JoinPhrase string `json:"join_phrase,omitempty"`
}

type CanonicalTrack struct {
	ReleaseTrackMBID string         `json:"release_track_mbid"`
	RecordingMBID    string         `json:"recording_mbid"`
	Title            string         `json:"title"`
	Disc             int            `json:"disc"`
	Track            int            `json:"track"`
	Number           string         `json:"number"`
	DurationMS       *int64         `json:"duration_ms,omitempty"`
	ArtistCredits    []ArtistCredit `json:"artist_credits"`
	ISRCs            []string       `json:"isrcs,omitempty"`
}

type CanonicalRelease struct {
	ReleaseMBID      string           `json:"release_mbid"`
	ReleaseGroupMBID string           `json:"release_group_mbid"`
	Title            string           `json:"title"`
	Date             string           `json:"date,omitempty"`
	Status           string           `json:"status,omitempty"`
	ArtistCredits    []ArtistCredit   `json:"artist_credits"`
	Tracks           []CanonicalTrack `json:"tracks"`
}

type CandidateTrack struct {
	RelativePath string `json:"relative_path"`
	Disc         int    `json:"disc"`
	Track        int    `json:"track"`
	DurationMS   int64  `json:"duration_ms"`
}

type TrackMatch struct {
	Candidate CandidateTrack `json:"candidate"`
	Canonical CanonicalTrack `json:"canonical"`
	Duration  DurationResult `json:"duration"`
}

type ReleaseMatch struct {
	Release       CanonicalRelease `json:"release"`
	Tracks        []TrackMatch     `json:"tracks"`
	TotalDuration *DurationResult  `json:"total_duration,omitempty"`
	Status        DurationStatus   `json:"status"`
}

func MatchRelease(policy DurationPolicy, explicitReleaseMBID string, release CanonicalRelease, candidates []CandidateTrack) (ReleaseMatch, error) {
	releaseID, err := CanonicalMBID(explicitReleaseMBID)
	if err != nil {
		return ReleaseMatch{}, fmt.Errorf("explicit release MBID: %w", err)
	}
	canonicalReleaseID, err := CanonicalMBID(release.ReleaseMBID)
	if err != nil || canonicalReleaseID != releaseID {
		return ReleaseMatch{}, fmt.Errorf("MusicBrainz response release does not match explicit target")
	}
	if len(candidates) != len(release.Tracks) || len(candidates) == 0 {
		return ReleaseMatch{}, fmt.Errorf("release track count mismatch: candidate=%d reference=%d", len(candidates), len(release.Tracks))
	}
	byPosition := make(map[[2]int]CanonicalTrack, len(release.Tracks))
	for _, track := range release.Tracks {
		if track.Disc <= 0 || track.Track <= 0 {
			return ReleaseMatch{}, fmt.Errorf("invalid canonical disc/track position")
		}
		key := [2]int{track.Disc, track.Track}
		if _, duplicate := byPosition[key]; duplicate {
			return ReleaseMatch{}, fmt.Errorf("duplicate canonical disc/track position %v", key)
		}
		if _, err := CanonicalMBID(track.RecordingMBID); err != nil {
			return ReleaseMatch{}, fmt.Errorf("recording MBID at %v: %w", key, err)
		}
		if _, err := CanonicalMBID(track.ReleaseTrackMBID); err != nil {
			return ReleaseMatch{}, fmt.Errorf("release-track MBID at %v: %w", key, err)
		}
		byPosition[key] = track
	}
	seen := make(map[[2]int]struct{}, len(candidates))
	result := ReleaseMatch{Release: release, Status: DurationAutoApprove}
	var observedTotal, referenceTotal int64
	allReferences := true
	for _, candidate := range candidates {
		key := [2]int{candidate.Disc, candidate.Track}
		if _, duplicate := seen[key]; duplicate {
			return ReleaseMatch{}, fmt.Errorf("duplicate candidate disc/track position %v", key)
		}
		seen[key] = struct{}{}
		canonical, ok := byPosition[key]
		if !ok {
			return ReleaseMatch{}, fmt.Errorf("candidate has no canonical track at %v", key)
		}
		duration, err := EvaluateTrackDuration(policy, canonical.DurationMS, candidate.DurationMS)
		if err != nil {
			return ReleaseMatch{}, err
		}
		result.Tracks = append(result.Tracks, TrackMatch{Candidate: candidate, Canonical: canonical, Duration: duration})
		result.Status = StrictestDurationStatus(result.Status, duration.Status)
		if candidate.DurationMS > math.MaxInt64-observedTotal {
			return ReleaseMatch{}, fmt.Errorf("observed release duration overflow")
		}
		observedTotal += candidate.DurationMS
		if canonical.DurationMS == nil {
			allReferences = false
		} else {
			if *canonical.DurationMS > math.MaxInt64-referenceTotal {
				return ReleaseMatch{}, fmt.Errorf("reference release duration overflow")
			}
			referenceTotal += *canonical.DurationMS
		}
	}
	if allReferences {
		total, err := EvaluateReleaseDuration(policy, &referenceTotal, observedTotal)
		if err != nil {
			return ReleaseMatch{}, err
		}
		result.TotalDuration = &total
		result.Status = StrictestDurationStatus(result.Status, total.Status)
	}
	return result, nil
}

func CanonicalMBID(value string) (string, error) {
	if value != strings.ToLower(value) || len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return "", fmt.Errorf("MBID must be a lowercase canonical UUID")
	}
	compact := strings.ReplaceAll(value, "-", "")
	decoded, err := hex.DecodeString(compact)
	if err != nil || len(decoded) != 16 {
		return "", fmt.Errorf("MBID must be a lowercase canonical UUID")
	}
	return value, nil
}
