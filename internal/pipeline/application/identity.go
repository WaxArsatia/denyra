package application

import (
	"context"
	"fmt"
	"strings"

	"github.com/waxarsatia/denyra/internal/pipeline/adapters/musicbrainz"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

type IdentityStatus string

const (
	IdentityNoMatch   IdentityStatus = "NO_MATCH"
	IdentityAmbiguous IdentityStatus = "AMBIGUOUS"
	IdentityExact     IdentityStatus = "EXACT"
	IdentityError     IdentityStatus = "ERROR"
)

type IdentityCandidate struct {
	Release  domain.CanonicalRelease `json:"release"`
	Match    domain.ReleaseMatch     `json:"match"`
	Evidence []musicbrainz.Evidence  `json:"evidence,omitempty"`
}

type IdentityDecision struct {
	Status     IdentityStatus         `json:"status"`
	Exact      *IdentityCandidate     `json:"exact,omitempty"`
	Candidates []IdentityCandidate    `json:"candidates,omitempty"`
	Evidence   []musicbrainz.Evidence `json:"evidence,omitempty"`
	Reason     string                 `json:"reason,omitempty"`
}

type ReleaseSearcher interface {
	SearchReleases(context.Context, musicbrainz.SearchInput) (musicbrainz.SearchResult, error)
}

type IdentityService struct {
	Search         ReleaseSearcher
	DurationPolicy domain.DurationPolicy
}

func (s IdentityService) Decide(ctx context.Context, plan domain.MetadataPlan, observed domain.TechnicalReleaseResult) (IdentityDecision, error) {
	if s.Search == nil {
		return IdentityDecision{Status: IdentityError, Reason: "MusicBrainz search is not configured"}, fmt.Errorf("MusicBrainz search is required")
	}
	if err := domain.ValidateMetadataPlan(plan); err != nil {
		return IdentityDecision{Status: IdentityError, Reason: err.Error()}, err
	}
	searchInput, taggedIDs, err := identitySearchInput(plan)
	if err != nil {
		return IdentityDecision{Status: IdentityError, Reason: err.Error()}, err
	}
	candidates, err := observedCandidates(plan, observed)
	if err != nil {
		return IdentityDecision{Status: IdentityError, Reason: err.Error()}, err
	}
	result, err := s.Search.SearchReleases(ctx, searchInput)
	if err != nil {
		return IdentityDecision{Status: IdentityError, Reason: err.Error()}, err
	}

	decision := IdentityDecision{Status: IdentityNoMatch, Evidence: result.Evidence, Reason: "no release satisfies identity and duration checks"}
	for _, release := range result.Releases {
		if len(taggedIDs) == 1 && release.ReleaseMBID != taggedIDs[0] {
			continue
		}
		if !metadataIdentical(plan, release) {
			continue
		}
		match, matchErr := domain.MatchRelease(s.DurationPolicy, release.ReleaseMBID, release, candidates)
		if matchErr != nil || match.Status != domain.DurationAutoApprove {
			continue
		}
		decision.Candidates = append(decision.Candidates, IdentityCandidate{Release: release, Match: match, Evidence: result.Evidence})
	}

	if len(taggedIDs) > 1 {
		decision.Status = IdentityAmbiguous
		decision.Reason = "conflicting tagged release MBIDs require manual choice"
		return decision, nil
	}
	switch len(decision.Candidates) {
	case 0:
		return decision, nil
	case 1:
		decision.Status = IdentityExact
		decision.Reason = "one release satisfies all identity and duration checks"
		decision.Exact = &decision.Candidates[0]
	default:
		decision.Status = IdentityAmbiguous
		decision.Reason = "multiple releases satisfy all identity and duration checks"
	}
	return decision, nil
}

func identitySearchInput(plan domain.MetadataPlan) (musicbrainz.SearchInput, []string, error) {
	input := musicbrainz.SearchInput{AlbumArtist: plan.AlbumArtist, Album: plan.Album, Date: plan.Date, TrackCount: plan.TrackTotal}
	for _, track := range plan.Tracks {
		input.ISRCs = append(input.ISRCs, track.ISRCs...)
		for key, values := range plan.Preserved[track.RelativePath] {
			switch {
			case strings.EqualFold(key, "MUSICBRAINZ_ALBUMID"):
				input.TaggedReleaseMBIDs = append(input.TaggedReleaseMBIDs, values...)
			case strings.EqualFold(key, "BARCODE"), strings.EqualFold(key, "UPC"):
				input.Barcodes = append(input.Barcodes, values...)
			case strings.EqualFold(key, "ISRC"):
				input.ISRCs = append(input.ISRCs, values...)
			}
		}
	}
	input.TaggedReleaseMBIDs = uniqueStrings(input.TaggedReleaseMBIDs)
	for _, id := range input.TaggedReleaseMBIDs {
		if _, err := domain.CanonicalMBID(id); err != nil {
			return musicbrainz.SearchInput{}, nil, fmt.Errorf("tagged release MBID %q: %w", id, err)
		}
	}
	input.Barcodes = uniqueStrings(input.Barcodes)
	input.ISRCs = uniqueStrings(input.ISRCs)
	return input, input.TaggedReleaseMBIDs, nil
}

func observedCandidates(plan domain.MetadataPlan, observed domain.TechnicalReleaseResult) ([]domain.CandidateTrack, error) {
	byPath := make(map[string]domain.TechnicalInfo, len(observed.Files))
	for _, file := range observed.Files {
		if _, duplicate := byPath[file.RelativePath]; duplicate {
			return nil, fmt.Errorf("duplicate technical evidence for %q", file.RelativePath)
		}
		byPath[file.RelativePath] = file.Info
	}
	result := make([]domain.CandidateTrack, 0, len(plan.Tracks))
	for _, track := range plan.Tracks {
		info, ok := byPath[track.RelativePath]
		if !ok || info.DurationMS <= 0 {
			return nil, fmt.Errorf("technical evidence missing for %q", track.RelativePath)
		}
		result = append(result, domain.CandidateTrack{RelativePath: track.RelativePath, Disc: track.Disc, Track: track.Track, DurationMS: info.DurationMS})
	}
	return result, nil
}

func metadataIdentical(plan domain.MetadataPlan, release domain.CanonicalRelease) bool {
	if folded(plan.Album) != folded(release.Title) || folded(plan.AlbumArtist) != folded(joinCredits(release.ArtistCredits)) || !sameReleaseDate(plan.Date, release.Date) || len(plan.Tracks) != len(release.Tracks) {
		return false
	}
	byPosition := make(map[[2]int]domain.CanonicalTrack, len(release.Tracks))
	for _, track := range release.Tracks {
		key := [2]int{track.Disc, track.Track}
		if _, duplicate := byPosition[key]; duplicate {
			return false
		}
		byPosition[key] = track
	}
	for _, local := range plan.Tracks {
		canonical, ok := byPosition[[2]int{local.Disc, local.Track}]
		if !ok || folded(local.Title) != folded(canonical.Title) || folded(local.Artist) != folded(joinCredits(canonical.ArtistCredits)) {
			return false
		}
		if len(local.ISRCs) > 0 && !intersectsFolded(local.ISRCs, canonical.ISRCs) {
			return false
		}
	}
	return true
}

func sameReleaseDate(left, right string) bool {
	left, right = strings.TrimSpace(left), strings.TrimSpace(right)
	if left == "" {
		return true
	}
	if right == "" {
		return false
	}
	return left == right || (len(left) >= 4 && len(right) >= 4 && left[:4] == right[:4])
}

func joinCredits(credits []domain.ArtistCredit) string {
	var result strings.Builder
	for _, credit := range credits {
		result.WriteString(credit.Name)
		result.WriteString(credit.JoinPhrase)
	}
	return result.String()
}

func folded(value string) string {
	return cases.Fold().String(norm.NFC.String(strings.TrimSpace(value)))
}

func intersectsFolded(left, right []string) bool {
	values := make(map[string]bool, len(left))
	for _, value := range left {
		values[folded(value)] = true
	}
	for _, value := range right {
		if values[folded(value)] {
			return true
		}
	}
	return false
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}
