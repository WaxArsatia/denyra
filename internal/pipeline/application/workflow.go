package application

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/waxarsatia/denyra/internal/contracts"
	denyrafs "github.com/waxarsatia/denyra/internal/pipeline/adapters/filesystem"
	"github.com/waxarsatia/denyra/internal/pipeline/adapters/musicbrainz"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

type WorkflowCompletion struct {
	Source                                                                   domain.Source
	SourcePath, EvidenceID, TargetReleaseMBID, DownloadID, SealedFingerprint string
	CompletedAt                                                              time.Time
	Provenance                                                               []byte
}

type WorkflowContext struct {
	TargetReleaseMBID, DownloadID string
	Release                       domain.CanonicalRelease
	Match                         domain.ReleaseMatch
	Technical                     domain.TechnicalReleaseResult
	Warnings                      []domain.Warning
}

type WorkflowStore interface {
	Candidate(context.Context, string) (domain.Candidate, error)
	TransitionCandidate(context.Context, string, uint64, domain.State, string, string, string, time.Time) (domain.TransitionEvent, error)
	Completion(context.Context, string) (WorkflowCompletion, error)
	ManualCompletion(context.Context, string) (WorkflowCompletion, error)
	SetWaitingResubmit(context.Context, string, uint64, string, time.Time) error
	SetWorkLocation(context.Context, string, string, uint64, time.Time) error
	TargetRelease(context.Context, string) (string, error)
	SaveWorkflow(context.Context, string, string, domain.CanonicalRelease, domain.ReleaseMatch, domain.TechnicalReleaseResult, []domain.Warning, string, time.Time) error
	Workflow(context.Context, string) (WorkflowContext, error)
	ImportIntentForCandidate(context.Context, string) (domain.ImportIntent, error)
	RecordTechnical(context.Context, string, string, domain.TechnicalReleaseResult, time.Time) error
	RecordMatch(context.Context, string, domain.ReleaseMatch, time.Time) error
	RecordEnrichment(context.Context, string, EnrichmentResult, time.Time) error
	RecordMutation(context.Context, string, MutationResult, time.Time) error
}

type ReleaseLookup interface {
	LookupRelease(context.Context, string) (domain.CanonicalRelease, musicbrainz.Evidence, error)
}

type ControlledWorkflow struct {
	Store                WorkflowStore
	Claim                ClaimService
	Validator            TechnicalValidator
	Lookup               ReleaseLookup
	Matching             MatchingService
	Enrichment           EnrichmentService
	Mutation             MutationService
	Quality              QualityReporter
	Import               ImportService
	SourceRoots          map[domain.Source]string
	MaxInlineTransitions int
	Now                  func() time.Time
}

func (workflow ControlledWorkflow) Process(ctx context.Context, item WorkItem) error {
	if workflow.Store == nil || workflow.MaxInlineTransitions <= 0 {
		return fmt.Errorf("controlled workflow store is required")
	}
	for step := 0; step < workflow.MaxInlineTransitions; step++ {
		candidate, err := workflow.Store.Candidate(ctx, item.CandidateID)
		if err != nil {
			return err
		}
		before := candidate.StateRevision
		if err := workflow.processOne(ctx, candidate); err != nil {
			return err
		}
		current, err := workflow.Store.Candidate(ctx, item.CandidateID)
		if err != nil {
			return err
		}
		if current.StateRevision == before || current.State == domain.StateArbitrationPending || current.State == domain.StateReviewRequired || current.State.Terminal() {
			return nil
		}
	}
	return fmt.Errorf("candidate %s exceeded bounded inline transitions", item.CandidateID)
}

func (workflow ControlledWorkflow) processOne(ctx context.Context, candidate domain.Candidate) error {
	switch candidate.State {
	case domain.StateReceived:
		return workflow.transition(ctx, candidate, domain.StateClaimed, "completion accepted for controlled claim", "")
	case domain.StateClaimed:
		return workflow.transition(ctx, candidate, domain.StateStabilizing, "release stability validation started", "")
	case domain.StateStabilizing:
		return workflow.claim(ctx, candidate)
	case domain.StateWorking:
		return workflow.transition(ctx, candidate, domain.StateTechnicalValidation, "technical hard gates started", "")
	case domain.StateTechnicalValidation:
		return workflow.validate(ctx, candidate)
	case domain.StateReleaseMatching:
		return workflow.match(ctx, candidate)
	case domain.StateEnriching:
		return workflow.enrichAndMutate(ctx, candidate)
	case domain.StateApproved:
		return workflow.publishApproval(ctx, candidate)
	case domain.StateImportReady:
		return workflow.submitImport(ctx, candidate)
	case domain.StateImportSubmitted:
		return workflow.transition(ctx, candidate, domain.StateImportReconciling, "Lidarr manual import submitted", "")
	case domain.StateImportReconciling:
		return workflow.reconcileImport(ctx, candidate)
	default:
		return nil
	}
}

func (workflow ControlledWorkflow) claim(ctx context.Context, candidate domain.Candidate) error {
	completion, err := workflow.completion(ctx, candidate)
	if err != nil {
		return err
	}
	root := workflow.SourceRoots[candidate.Source]
	result, err := workflow.Claim.Claim(ctx, candidate.ID, CompletionEvidence{ID: completion.EvidenceID, Source: candidate.Source, SourceRoot: root, CompletedPath: completion.SourcePath, CompletedAt: completion.CompletedAt, SealedFingerprint: completion.SealedFingerprint})
	if errors.Is(err, ErrWaitingResubmit) {
		return workflow.Store.SetWaitingResubmit(ctx, candidate.ID, candidate.StateRevision, err.Error(), workflow.now())
	}
	if err != nil {
		return err
	}
	return workflow.Store.SetWorkLocation(ctx, candidate.ID, result.WorkPath, candidate.StateRevision, workflow.now())
}

func (workflow ControlledWorkflow) validate(ctx context.Context, candidate domain.Candidate) error {
	paths, err := releaseFiles(filepath.Join(workflow.Claim.WorkRoot, candidate.ID))
	if err != nil {
		return err
	}
	result := workflow.Validator.Validate(ctx, filepath.Join(workflow.Claim.WorkRoot, candidate.ID), paths)
	if err := workflow.Store.RecordTechnical(ctx, candidate.ID, filepath.Join(workflow.Claim.WorkRoot, candidate.ID), result, workflow.now()); err != nil {
		return err
	}
	completion, _ := workflow.completion(ctx, candidate)
	if err := workflow.Store.SaveWorkflow(ctx, candidate.ID, completion.TargetReleaseMBID, domain.CanonicalRelease{}, domain.ReleaseMatch{}, result, result.Warnings, completion.DownloadID, workflow.now()); err != nil {
		return err
	}
	if result.Retryable {
		return fmt.Errorf("retryable technical validation: %s", result.Reason)
	}
	if result.Rejected {
		if err := moveCandidate(workflow.Claim.WorkRoot, workflow.Matching.QuarantineRoot, candidate.ID); err != nil {
			return err
		}
		return workflow.transition(ctx, candidate, domain.StateRejected, result.Reason, "")
	}
	return workflow.transition(ctx, candidate, domain.StateReleaseMatching, "technical hard gates passed", "")
}

func (workflow ControlledWorkflow) match(ctx context.Context, candidate domain.Candidate) error {
	target, err := workflow.Store.TargetRelease(ctx, candidate.ID)
	if err != nil {
		return err
	}
	if target == "" {
		if err := moveCandidate(workflow.Claim.WorkRoot, workflow.Matching.QuarantineRoot, candidate.ID); err != nil {
			return err
		}
		return workflow.transition(ctx, candidate, domain.StateReviewRequired, "explicit MusicBrainz Release ID required", "")
	}
	release, _, err := workflow.Lookup.LookupRelease(ctx, target)
	if err != nil {
		return err
	}
	contextValue, err := workflow.Store.Workflow(ctx, candidate.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	tracks, err := candidateTracks(contextValue.Technical)
	if err != nil {
		decision, moveErr := workflow.Matching.quarantine(candidate.ID, domain.StateReviewRequired, err.Error(), domain.ReleaseMatch{})
		if moveErr != nil {
			return moveErr
		}
		return workflow.transition(ctx, candidate, decision.State, decision.Reason, target)
	}
	decision, err := workflow.Matching.Evaluate(candidate.ID, target, release, tracks)
	if err != nil {
		return err
	}
	if err := workflow.Store.SaveWorkflow(ctx, candidate.ID, target, release, decision.Match, contextValue.Technical, contextValue.Warnings, contextValue.DownloadID, workflow.now()); err != nil {
		return err
	}
	if err := workflow.Store.RecordMatch(ctx, candidate.ID, decision.Match, workflow.now()); err != nil {
		return err
	}
	return workflow.transition(ctx, candidate, decision.State, defaultReason(decision.Reason, "release-atomic MusicBrainz match passed"), target)
}

func (workflow ControlledWorkflow) enrichAndMutate(ctx context.Context, candidate domain.Candidate) error {
	state, err := workflow.Store.Workflow(ctx, candidate.ID)
	if err != nil {
		return err
	}
	tracks := make([]EnrichmentTrack, 0, len(state.Match.Tracks))
	plans := make(map[string]domain.TagSet, len(state.Match.Tracks))
	for _, match := range state.Match.Tracks {
		artists := match.Canonical.ArtistCredits
		if len(artists) == 0 {
			artists = state.Release.ArtistCredits
		}
		artistName := joinedCredits(artists)
		tracks = append(tracks, EnrichmentTrack{RelativeFLAC: match.Candidate.RelativePath, Query: domain.LyricsQuery{TrackName: match.Canonical.Title, ArtistName: artistName, AlbumName: state.Release.Title, DurationMS: match.Candidate.DurationMS}})
		tags, tagErr := domain.CanonicalTags(domain.TagInput{Title: match.Canonical.Title, Artists: artists, Album: state.Release.Title, AlbumArtists: state.Release.ArtistCredits, TrackNumber: match.Canonical.Track, DiscNumber: match.Canonical.Disc, Date: state.Release.Date, Genres: []string{}, ISRCs: match.Canonical.ISRCs, RecordingMBID: match.Canonical.RecordingMBID, ReleaseTrackMBID: match.Canonical.ReleaseTrackMBID, ReleaseMBID: state.Release.ReleaseMBID, ReleaseGroupMBID: state.Release.ReleaseGroupMBID})
		if tagErr != nil {
			return tagErr
		}
		plans[match.Candidate.RelativePath] = tags
	}
	enrichment, err := workflow.Enrichment.Enrich(ctx, candidate.ID, state.TargetReleaseMBID, tracks)
	if err != nil {
		return err
	}
	if err := workflow.Store.RecordEnrichment(ctx, candidate.ID, enrichment, workflow.now()); err != nil {
		return err
	}
	warnings := append(append([]domain.Warning(nil), state.Warnings...), enrichment.Warnings...)
	mutation, err := workflow.Mutation.MutateRelease(ctx, candidate.ID, plans)
	if err != nil {
		return err
	}
	if err := workflow.Store.RecordMutation(ctx, candidate.ID, mutation, workflow.now()); err != nil {
		return err
	}
	if err := workflow.Store.SaveWorkflow(ctx, candidate.ID, state.TargetReleaseMBID, state.Release, state.Match, state.Technical, warnings, state.DownloadID, workflow.now()); err != nil {
		return err
	}
	if mutation.Quarantined {
		return workflow.transition(ctx, candidate, domain.StateQuarantined, mutation.Reason, state.TargetReleaseMBID)
	}
	return workflow.transition(ctx, candidate, domain.StateApproved, "deterministic mutation and post-mutation integrity passed", state.TargetReleaseMBID)
}

func (workflow ControlledWorkflow) publishApproval(ctx context.Context, candidate domain.Candidate) error {
	state, err := workflow.Store.Workflow(ctx, candidate.ID)
	if err != nil {
		return err
	}
	if candidate.Source == domain.SourceManual {
		return workflow.transition(ctx, candidate, domain.StateImportReady, "manual candidate approved for controlled import", state.TargetReleaseMBID)
	}
	warnings := make([]contracts.Warning, 0, len(state.Warnings))
	for _, warning := range state.Warnings {
		class := contracts.NonBlockingWarning
		if warning.Kind == domain.WarningQuality {
			class = contracts.QualityWarning
		}
		warnings = append(warnings, contracts.Warning{Class: class, Code: warning.Code, Message: warning.Details})
	}
	quality := qualityVector(candidate.Source, state.Technical, state.Warnings)
	request := contracts.CandidateApproved{RequestID: "quality-" + candidate.ID, JobID: candidate.GatewayJobID, CandidateID: candidate.ID, ConfigSnapshotID: candidate.ConfigSnapshotID, MusicBrainzReleaseID: state.TargetReleaseMBID, ApprovedAt: candidate.UpdatedAt, Quality: ContractQuality(quality), Warnings: warnings, StateRevision: candidate.StateRevision}
	if err := workflow.Quality.Report(ctx, request, request.RequestID); err != nil {
		return err
	}
	current, err := workflow.Store.Candidate(ctx, candidate.ID)
	if err != nil {
		return err
	}
	if current.State != domain.StateApproved {
		return nil
	}
	return workflow.transition(ctx, current, domain.StateArbitrationPending, "quality evidence accepted by acquisition gateway", state.TargetReleaseMBID)
}

func (workflow ControlledWorkflow) submitImport(ctx context.Context, candidate domain.Candidate) error {
	state, err := workflow.Store.Workflow(ctx, candidate.ID)
	if err != nil {
		return err
	}
	authorization := ImportGatewayWinner
	if candidate.Source == domain.SourceManual {
		authorization = ImportManualApproved
	}
	if _, existingErr := workflow.Store.ImportIntentForCandidate(ctx, candidate.ID); existingErr == nil {
		return workflow.transition(ctx, candidate, domain.StateImportSubmitted, "reconciled existing Lidarr import intent", state.TargetReleaseMBID)
	} else if !errors.Is(existingErr, sql.ErrNoRows) {
		return existingErr
	}
	if _, err := workflow.Import.Submit(ctx, candidate.ID, state.TargetReleaseMBID, state.DownloadID, authorization); err != nil {
		return err
	}
	return workflow.transition(ctx, candidate, domain.StateImportSubmitted, "release batch handed to Lidarr Manual Import API", state.TargetReleaseMBID)
}

func (workflow ControlledWorkflow) reconcileImport(ctx context.Context, candidate domain.Candidate) error {
	intent, err := workflow.Store.ImportIntentForCandidate(ctx, candidate.ID)
	if err != nil {
		return err
	}
	verification, err := workflow.Import.Reconcile(ctx, intent)
	if err != nil || !verification.Complete {
		return err
	}
	return workflow.transition(ctx, candidate, domain.StateImported, "final Lidarr library paths and sidecars verified", intent.TargetReleaseMBID)
}

func (workflow ControlledWorkflow) completion(ctx context.Context, candidate domain.Candidate) (WorkflowCompletion, error) {
	if candidate.Source == domain.SourceManual {
		return workflow.Store.ManualCompletion(ctx, candidate.ID)
	}
	return workflow.Store.Completion(ctx, candidate.ID)
}

func (workflow ControlledWorkflow) transition(ctx context.Context, candidate domain.Candidate, to domain.State, reason, target string) error {
	_, err := workflow.Store.TransitionCandidate(ctx, candidate.ID, candidate.StateRevision, to, "pipeline-worker", reason, target, workflow.now())
	return err
}

func (workflow ControlledWorkflow) now() time.Time {
	if workflow.Now != nil {
		return workflow.Now().UTC()
	}
	return time.Now().UTC()
}

func releaseFiles(root string) ([]string, error) {
	var result []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		result = append(result, filepath.ToSlash(relative))
		return nil
	})
	sort.Strings(result)
	return result, err
}

func candidateTracks(technical domain.TechnicalReleaseResult) ([]domain.CandidateTrack, error) {
	result := make([]domain.CandidateTrack, 0, len(technical.Files))
	for _, file := range technical.Files {
		disc, track, err := taggedPosition(file.OriginalComments)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file.RelativePath, err)
		}
		result = append(result, domain.CandidateTrack{RelativePath: file.RelativePath, Disc: disc, Track: track, DurationMS: file.Info.DurationMS})
	}
	return result, nil
}

func taggedPosition(tags map[string][]string) (int, int, error) {
	parse := func(field string, fallback int) (int, error) {
		values := tags[field]
		if len(values) == 0 && fallback > 0 {
			return fallback, nil
		}
		if len(values) == 0 {
			return 0, fmt.Errorf("%s is required", field)
		}
		position := 0
		for _, raw := range values {
			value := strings.SplitN(raw, "/", 2)[0]
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || parsed <= 0 || position != 0 && parsed != position {
				return 0, fmt.Errorf("ambiguous or invalid %s", field)
			}
			position = parsed
		}
		return position, nil
	}
	disc, err := parse("DISCNUMBER", 1)
	if err != nil {
		return 0, 0, err
	}
	track, err := parse("TRACKNUMBER", 0)
	return disc, track, err
}

func moveCandidate(fromRoot, toRoot, id string) error {
	if err := os.MkdirAll(toRoot, 0o750); err != nil {
		return err
	}
	return denyrafs.MoveAtomic(filepath.Join(fromRoot, id), filepath.Join(toRoot, id))
}

func joinedCredits(credits []domain.ArtistCredit) string {
	var builder strings.Builder
	for _, credit := range credits {
		builder.WriteString(credit.Name)
		builder.WriteString(credit.JoinPhrase)
	}
	return builder.String()
}

func qualityVector(source domain.Source, technical domain.TechnicalReleaseResult, warnings []domain.Warning) domain.QualityVector {
	bitDepth, sampleRate := int(^uint(0)>>1), int(^uint(0)>>1)
	for _, file := range technical.Files {
		if file.Info.BitDepth < bitDepth {
			bitDepth = file.Info.BitDepth
		}
		if file.Info.SampleRate < sampleRate {
			sampleRate = file.Info.SampleRate
		}
	}
	if bitDepth == int(^uint(0)>>1) {
		bitDepth = 0
	}
	if sampleRate == int(^uint(0)>>1) {
		sampleRate = 0
	}
	confidence := map[domain.Source]int{domain.SourceManual: 100, domain.SourceSlskd: 90, domain.SourceSpotiFLAC: 70, domain.SourceOther: 60}[source]
	return domain.QualityVector{IdentityRank: 100, EditionRank: 100, QualityWarningCount: domain.CountQualityWarnings(warnings), SourceConfidence: confidence, BitDepth: bitDepth, SampleRate: sampleRate}
}

func defaultReason(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func canonicalJSON(value any) []byte { encoded, _ := json.Marshal(value); return encoded }
