package domain

import (
	"testing"
	"time"
)

func TestArbitrationQualityOrder(t *testing.T) {
	t.Parallel()
	base := Quality{IdentityRank: 4, EditionRank: 2, QualityWarningCount: 0, SourceConfidence: 90, BitDepth: 24, SampleRate: 96_000}
	tests := []struct {
		name   string
		change func(*Quality)
	}{
		{name: "identity outranks every technical value", change: func(value *Quality) { value.IdentityRank--; value.BitDepth = 32; value.SampleRate = 384_000 }},
		{name: "edition outranks warnings and technical values", change: func(value *Quality) { value.EditionRank--; value.QualityWarningCount = 0; value.SourceConfidence = 100 }},
		{name: "quality warning absence", change: func(value *Quality) { value.QualityWarningCount++ }},
		{name: "source confidence", change: func(value *Quality) { value.SourceConfidence-- }},
		{name: "bit depth", change: func(value *Quality) { value.BitDepth-- }},
		{name: "sample rate", change: func(value *Quality) { value.SampleRate-- }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			worse := base
			test.change(&worse)
			if CompareQuality(base, worse) <= 0 || CompareQuality(worse, base) >= 0 {
				t.Fatalf("comparison did not rank base above changed value: base=%+v changed=%+v", base, worse)
			}
		})
	}
}

func TestArbitrationTieBreakAndDeadlineBoundary(t *testing.T) {
	t.Parallel()
	firstApproved := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	deadline := firstApproved.Add(30 * time.Minute)
	quality := Quality{IdentityRank: 4, EditionRank: 2, SourceConfidence: 90, BitDepth: 24, SampleRate: 96_000}
	first := ApprovedCandidate{ID: "spotiflac", Source: CandidateSourceSpotiFLAC, ApprovedAt: firstApproved, CompletionAt: firstApproved.Add(-time.Minute), Quality: quality}
	primary := ApprovedCandidate{ID: "slskd", Source: CandidateSourceSlskd, ApprovedAt: deadline.Add(-time.Nanosecond), CompletionAt: firstApproved, Quality: quality}

	decision, ok := DecideArbitration([]ApprovedCandidate{first}, deadline, deadline.Add(-time.Nanosecond))
	if ok || decision.Winner.ID != "" {
		t.Fatal("single candidate locked before deadline")
	}
	decision, ok = DecideArbitration([]ApprovedCandidate{first, primary}, deadline, deadline.Add(-time.Nanosecond))
	if !ok || decision.Winner.ID != primary.ID || decision.Reason != DecisionProvenancePriority {
		t.Fatalf("primary provenance tie-break decision=%+v ok=%v", decision, ok)
	}

	atBoundary := primary
	atBoundary.ApprovedAt = deadline
	decision, ok = DecideArbitration([]ApprovedCandidate{first, atBoundary}, deadline, deadline)
	if !ok || decision.Winner.ID != first.ID || decision.Reason != DecisionDeadlineExpired {
		t.Fatalf("deadline boundary decision=%+v ok=%v", decision, ok)
	}
}

func TestArbitrationIgnoresNonBlockingWarningsAndUsesCompletionTimeLast(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	quality := Quality{IdentityRank: 4, EditionRank: 2, SourceConfidence: 90, BitDepth: 24, SampleRate: 96_000}
	later := ApprovedCandidate{ID: "later", Source: CandidateSourceSpotiFLAC, ApprovedAt: now, CompletionAt: now, Quality: quality, NonBlockingWarningCount: 50}
	earlier := ApprovedCandidate{ID: "earlier", Source: CandidateSourceSpotiFLAC, ApprovedAt: now.Add(time.Second), CompletionAt: now.Add(-time.Second), Quality: quality}
	decision, ok := DecideArbitration([]ApprovedCandidate{later, earlier}, now.Add(time.Minute), now.Add(2*time.Second))
	if !ok || decision.Winner.ID != earlier.ID || decision.Reason != DecisionCompletionTime {
		t.Fatalf("completion tie-break decision=%+v ok=%v", decision, ok)
	}
}
