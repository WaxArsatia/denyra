package domain_test

import (
	"testing"

	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

func TestQualityComparisonIsLexicographicInApprovedOrder(t *testing.T) {
	base := domain.QualityVector{IdentityRank: 4, EditionRank: 2, QualityWarningCount: 0, SourceConfidence: 90, BitDepth: 16, SampleRate: 44_100}
	if domain.CompareQuality(domain.QualityVector{IdentityRank: 3, EditionRank: 99, BitDepth: 32, SampleRate: 384_000}, base) >= 0 {
		t.Fatal("bit depth/sample rate outranked identity correctness")
	}
	if domain.CompareQuality(domain.QualityVector{IdentityRank: 4, EditionRank: 1, BitDepth: 32, SampleRate: 384_000}, base) >= 0 {
		t.Fatal("technical resolution outranked edition")
	}
	warned := base
	warned.QualityWarningCount = 1
	warned.BitDepth = 32
	if domain.CompareQuality(warned, base) >= 0 {
		t.Fatal("higher resolution compensated for quality warning")
	}
	if domain.CompareQuality(base, base) != 0 {
		t.Fatal("equal vectors do not compare equal")
	}
}

func TestOnlyQualityWarningsEnterQualityVector(t *testing.T) {
	warnings := []domain.Warning{
		{Kind: domain.WarningQuality, Code: "POSSIBLE_LOSSY"},
		{Kind: domain.WarningNonBlocking, Code: "LYRICS_UNAVAILABLE"},
		{Kind: domain.WarningNonBlocking, Code: "ARTWORK_UNAVAILABLE"},
	}
	if got := domain.CountQualityWarnings(warnings); got != 1 {
		t.Fatalf("quality warning count = %d", got)
	}
}
