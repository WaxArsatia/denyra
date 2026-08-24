package application

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	denyrafs "github.com/waxarsatia/denyra/internal/pipeline/adapters/filesystem"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

type SubmissionPreviewStore interface {
	Submission(context.Context, string) (SubmissionRecord, error)
	CachedSubmissionPreview(context.Context, string, string) (domain.SubmissionPreview, bool, error)
	PutSubmissionPreview(context.Context, domain.SubmissionPreview, time.Time) error
	SaveSubmissionDraft(context.Context, string, domain.SubmissionDecision, time.Time) error
}

type PreviewInspector interface {
	Inspect(context.Context, string) (domain.TechnicalInfo, map[string][]string, domain.CommandEvidence, error)
}

type SubmissionPreviewService struct {
	Store     SubmissionPreviewStore
	Inspector PreviewInspector
	Identity  *IdentityService
	Artwork   *ArtworkService
	Scan      func(string) (denyrafs.Tree, error)
	Now       func() time.Time
}

func (s SubmissionPreviewService) Preview(ctx context.Context, submissionID string, refresh bool) (domain.SubmissionPreview, error) {
	if s.Store == nil || s.Inspector == nil {
		return domain.SubmissionPreview{}, fmt.Errorf("submission preview service is not configured")
	}
	record, err := s.Store.Submission(ctx, submissionID)
	if err != nil {
		return domain.SubmissionPreview{}, err
	}
	scan := s.Scan
	if scan == nil {
		scan = denyrafs.Scan
	}
	tree, err := scan(record.SourcePath)
	if err != nil {
		return domain.SubmissionPreview{}, err
	}
	if !refresh {
		if cached, found, err := s.Store.CachedSubmissionPreview(ctx, submissionID, tree.Fingerprint); err != nil {
			return domain.SubmissionPreview{}, err
		} else if found {
			return cached, nil
		}
	}
	preview := domain.SubmissionPreview{SubmissionID: submissionID, Ingress: record.Ingress, Revision: record.Revision, Fingerprint: tree.Fingerprint}
	observed := domain.TechnicalReleaseResult{}
	var albumArtists, albums, dates []fieldObservation
	for _, entry := range tree.Entries {
		if !strings.EqualFold(filepath.Ext(entry.RelativePath), ".flac") {
			continue
		}
		info, comments, _, err := s.Inspector.Inspect(ctx, filepath.Join(record.SourcePath, filepath.FromSlash(entry.RelativePath)))
		if err != nil {
			return domain.SubmissionPreview{}, fmt.Errorf("inspect %s: %w", entry.RelativePath, err)
		}
		track := domain.TrackMetadata{RelativePath: entry.RelativePath, DurationMS: info.DurationMS, ISRCs: append([]string(nil), comments["ISRC"]...)}
		observed.Files = append(observed.Files, domain.FileTechnicalEvidence{RelativePath: entry.RelativePath, Info: info, OriginalComments: cloneComments(comments)})
		track.Title = oneTrackValue(comments, "TITLE", entry.RelativePath, &preview.Conflicts)
		track.Artist = oneTrackValue(comments, "ARTIST", entry.RelativePath, &preview.Conflicts)
		track.Track, _ = positionValue(comments, "TRACKNUMBER", entry.RelativePath, &preview.Conflicts)
		track.Disc, _ = positionValue(comments, "DISCNUMBER", entry.RelativePath, &preview.Conflicts)
		preview.Metadata.Tracks = append(preview.Metadata.Tracks, track)
		if preview.Metadata.Preserved == nil {
			preview.Metadata.Preserved = make(map[string]map[string][]string)
		}
		preview.Metadata.Preserved[entry.RelativePath] = cloneComments(comments)
		albumArtists = append(albumArtists, observe(comments, "ALBUMARTIST", entry.RelativePath))
		albums = append(albums, observe(comments, "ALBUM", entry.RelativePath))
		dates = append(dates, observe(comments, "DATE", entry.RelativePath))
	}
	if len(preview.Metadata.Tracks) == 0 {
		return domain.SubmissionPreview{}, fmt.Errorf("submission contains no FLAC files")
	}
	preview.Metadata.AlbumArtist = consensusValue("ALBUMARTIST", albumArtists, true, &preview.Conflicts)
	preview.Metadata.Album = consensusValue("ALBUM", albums, true, &preview.Conflicts)
	preview.Metadata.Date = consensusValue("DATE", dates, false, &preview.Conflicts)
	preview.Metadata.TrackTotal = len(preview.Metadata.Tracks)
	for _, track := range preview.Metadata.Tracks {
		if track.Disc > preview.Metadata.DiscTotal {
			preview.Metadata.DiscTotal = track.Disc
		}
	}
	if preview.Metadata.DiscTotal == 0 {
		preview.Metadata.DiscTotal = 1
	}
	identity := IdentityDecision{}
	if s.Identity != nil {
		if validationErr := domain.ValidateMetadataPlan(preview.Metadata); validationErr == nil {
			identity, _ = s.Identity.Decide(ctx, preview.Metadata, observed)
			preview.Identity = identityPreview(identity)
		}
	}
	if s.Artwork != nil {
		selection, evidence, artworkErr := s.Artwork.Select(ctx, submissionID, record.SourcePath, preview.Metadata.Preserved, identity)
		if artworkErr == nil {
			preview.Artwork = selection
			for _, item := range evidence {
				item.ResponseBody = nil
				preview.ArtworkEvidence = append(preview.ArtworkEvidence, item)
			}
		}
	}
	if err := s.Store.PutSubmissionPreview(ctx, preview, s.now()); err != nil {
		return domain.SubmissionPreview{}, err
	}
	return preview, nil
}

func (s SubmissionPreviewService) ReplaceArtwork(ctx context.Context, submissionID string, body io.Reader) (domain.SubmissionPreview, error) {
	if s.Artwork == nil {
		return domain.SubmissionPreview{}, fmt.Errorf("artwork service is not configured")
	}
	if _, err := s.Artwork.Replace(ctx, submissionID, body); err != nil {
		return domain.SubmissionPreview{}, err
	}
	return s.Preview(ctx, submissionID, true)
}

func (s SubmissionPreviewService) ArtworkPath(submissionID string) (string, error) {
	if s.Artwork == nil {
		return "", fmt.Errorf("artwork service is not configured")
	}
	return s.Artwork.Path(submissionID)
}

func (s SubmissionPreviewService) ArtworkMaxBytes() int64 {
	if s.Artwork == nil {
		return 0
	}
	return s.Artwork.MaxBytes
}

func identityPreview(decision IdentityDecision) *domain.IdentityPreview {
	preview := &domain.IdentityPreview{Status: string(decision.Status), Reason: decision.Reason}
	switch decision.Status {
	case IdentityExact:
		preview.SuggestedDestination = domain.DestinationManaged
		if decision.Exact != nil {
			preview.ExactReleaseMBID = decision.Exact.Release.ReleaseMBID
		}
	case IdentityNoMatch:
		preview.SuggestedDestination = domain.DestinationUnmanaged
	}
	for _, candidate := range decision.Candidates {
		preview.Candidates = append(preview.Candidates, domain.IdentityCandidatePreview{
			ReleaseMBID: candidate.Release.ReleaseMBID,
			Title:       candidate.Release.Title,
			Artist:      joinCredits(candidate.Release.ArtistCredits),
			Date:        candidate.Release.Date,
			MatchStatus: candidate.Match.Status,
		})
	}
	for _, evidence := range decision.Evidence {
		preview.Evidence = append(preview.Evidence, domain.IdentityEvidence{Endpoint: evidence.Endpoint, StatusCode: evidence.StatusCode, ResponseSHA256: evidence.ResponseSHA256})
	}
	return preview
}

func (s SubmissionPreviewService) SaveDraft(ctx context.Context, submissionID string, decision domain.SubmissionDecision) error {
	if err := domain.ValidateSubmissionDecision(decision); err != nil {
		return err
	}
	preview, err := s.Preview(ctx, submissionID, false)
	if err != nil {
		return err
	}
	if preview.Fingerprint != decision.PreviewFingerprint {
		return ErrPreviewChanged
	}
	return s.Store.SaveSubmissionDraft(ctx, submissionID, decision, s.now())
}

func (s SubmissionPreviewService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

type fieldObservation struct {
	path   string
	values []string
}

func observe(comments map[string][]string, field, path string) fieldObservation {
	return fieldObservation{path: path, values: cleanValues(comments[field])}
}

func oneTrackValue(comments map[string][]string, field, path string, conflicts *[]domain.MetadataConflict) string {
	values := cleanValues(comments[field])
	if len(values) != 1 {
		*conflicts = append(*conflicts, domain.MetadataConflict{Field: field, RelativePath: path, Values: values})
		return ""
	}
	return values[0]
}

func positionValue(comments map[string][]string, field, path string, conflicts *[]domain.MetadataConflict) (int, int) {
	value := oneTrackValue(comments, field, path, conflicts)
	if value == "" {
		return 0, 0
	}
	positionText, totalText, _ := strings.Cut(value, "/")
	position, err := strconv.Atoi(strings.TrimSpace(positionText))
	if err != nil || position <= 0 {
		*conflicts = append(*conflicts, domain.MetadataConflict{Field: field, RelativePath: path, Values: []string{value}})
		return 0, 0
	}
	total := 0
	if totalText != "" {
		total, err = strconv.Atoi(strings.TrimSpace(totalText))
		if err != nil || total < position {
			*conflicts = append(*conflicts, domain.MetadataConflict{Field: field, RelativePath: path, Values: []string{value}})
			return 0, 0
		}
	}
	return position, total
}

func consensusValue(field string, observations []fieldObservation, required bool, conflicts *[]domain.MetadataConflict) string {
	unique := make(map[string]bool)
	missing := false
	for _, observation := range observations {
		if len(observation.values) != 1 {
			missing = true
			continue
		}
		unique[observation.values[0]] = true
	}
	if len(unique) == 0 && !required {
		return ""
	}
	if len(unique) != 1 || (required && missing) {
		values := make([]string, 0, len(unique))
		for value := range unique {
			values = append(values, value)
		}
		sort.Strings(values)
		*conflicts = append(*conflicts, domain.MetadataConflict{Field: field, Values: values})
		return ""
	}
	for value := range unique {
		return value
	}
	return ""
}

func cleanValues(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]bool)
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func cloneComments(comments map[string][]string) map[string][]string {
	result := make(map[string][]string, len(comments))
	for field, values := range comments {
		result[field] = append([]string(nil), values...)
	}
	return result
}
