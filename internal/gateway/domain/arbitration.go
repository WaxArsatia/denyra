package domain

import (
	"sort"
	"time"
)

type Quality struct {
	IdentityRank        int `json:"identity_rank"`
	EditionRank         int `json:"edition_rank"`
	QualityWarningCount int `json:"quality_warning_count"`
	SourceConfidence    int `json:"source_confidence"`
	BitDepth            int `json:"bit_depth"`
	SampleRate          int `json:"sample_rate"`
}

type CandidateSource string

const (
	CandidateSourceSlskd     CandidateSource = "slskd"
	CandidateSourceSpotiFLAC CandidateSource = "spotiflac"
)

func CompareQuality(left, right Quality) int {
	comparisons := [][2]int{
		{left.IdentityRank, right.IdentityRank},
		{left.EditionRank, right.EditionRank},
		{-left.QualityWarningCount, -right.QualityWarningCount},
		{left.SourceConfidence, right.SourceConfidence},
		{left.BitDepth, right.BitDepth},
		{left.SampleRate, right.SampleRate},
	}
	for _, values := range comparisons {
		if values[0] > values[1] {
			return 1
		}
		if values[0] < values[1] {
			return -1
		}
	}
	return 0
}

type ApprovedCandidate struct {
	ID                      string          `json:"candidate_id"`
	Source                  CandidateSource `json:"source"`
	ApprovedAt              time.Time       `json:"approved_at"`
	CompletionAt            time.Time       `json:"completion_at"`
	StateRevision           uint64          `json:"state_revision"`
	Quality                 Quality         `json:"quality"`
	NonBlockingWarningCount int             `json:"non_blocking_warning_count"`
}

type DecisionReason string

const (
	DecisionQuality            DecisionReason = "QUALITY_VECTOR"
	DecisionProvenancePriority DecisionReason = "PROVENANCE_PRIORITY"
	DecisionCompletionTime     DecisionReason = "COMPLETION_TIMESTAMP"
	DecisionCandidateID        DecisionReason = "CANDIDATE_ID"
	DecisionDeadlineExpired    DecisionReason = "ARBITRATION_DEADLINE_EXPIRED"
)

type ArbitrationDecision struct {
	Winner ApprovedCandidate
	Losers []ApprovedCandidate
	Reason DecisionReason
}

func DecideArbitration(candidates []ApprovedCandidate, deadline, now time.Time) (ArbitrationDecision, bool) {
	eligible := make([]ApprovedCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.ApprovedAt.Before(deadline) {
			eligible = append(eligible, candidate)
		}
	}
	if len(eligible) == 0 || len(eligible) == 1 && now.Before(deadline) {
		return ArbitrationDecision{}, false
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		return eligible[i].ApprovedAt.Before(eligible[j].ApprovedAt)
	})
	reason := DecisionDeadlineExpired
	if len(eligible) > 1 {
		reason = compareApproved(eligible[0], eligible[1])
		sort.SliceStable(eligible, func(i, j int) bool {
			return betterApproved(eligible[i], eligible[j])
		})
	}
	return ArbitrationDecision{Winner: eligible[0], Losers: append([]ApprovedCandidate(nil), eligible[1:]...), Reason: reason}, true
}

func betterApproved(left, right ApprovedCandidate) bool {
	if quality := CompareQuality(left.Quality, right.Quality); quality != 0 {
		return quality > 0
	}
	if sourcePriority(left.Source) != sourcePriority(right.Source) {
		return sourcePriority(left.Source) > sourcePriority(right.Source)
	}
	if !left.CompletionAt.Equal(right.CompletionAt) {
		return left.CompletionAt.Before(right.CompletionAt)
	}
	return left.ID < right.ID
}

func compareApproved(left, right ApprovedCandidate) DecisionReason {
	if CompareQuality(left.Quality, right.Quality) != 0 {
		return DecisionQuality
	}
	if sourcePriority(left.Source) != sourcePriority(right.Source) {
		return DecisionProvenancePriority
	}
	if !left.CompletionAt.Equal(right.CompletionAt) {
		return DecisionCompletionTime
	}
	return DecisionCandidateID
}

func sourcePriority(source CandidateSource) int {
	if source == CandidateSourceSlskd {
		return 2
	}
	if source == CandidateSourceSpotiFLAC {
		return 1
	}
	return 0
}
