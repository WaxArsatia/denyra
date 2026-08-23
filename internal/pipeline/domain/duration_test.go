package domain_test

import (
	"math"
	"testing"

	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

func TestTrackDurationBoundariesUseIntegerMilliseconds(t *testing.T) {
	reference := int64(600_000) // auto=max(5s,12s), review=max(15s,30s)
	for _, test := range []struct {
		name       string
		observed   int64
		want       domain.DurationStatus
		difference int64
	}{
		{"below auto", reference + 11_999, domain.DurationAutoApprove, 11_999},
		{"equal auto", reference + 12_000, domain.DurationAutoApprove, 12_000},
		{"above auto", reference + 12_001, domain.DurationManualReview, 12_001},
		{"equal review", reference + 30_000, domain.DurationManualReview, 30_000},
		{"above review", reference + 30_001, domain.DurationReject, 30_001},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := domain.EvaluateTrackDuration(approvedDurationPolicy(), &reference, test.observed)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != test.want || result.DifferenceMS != test.difference || result.AutoThresholdMS != 12_000 || result.ReviewThresholdMS != 30_000 {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

func TestTrackDurationUsesFloorsAndMissingReferenceNeedsReview(t *testing.T) {
	reference := int64(100_000)
	auto, err := domain.EvaluateTrackDuration(approvedDurationPolicy(), &reference, reference+5_000)
	if err != nil {
		t.Fatal(err)
	}
	if auto.Status != domain.DurationAutoApprove || auto.AutoThresholdMS != 5_000 || auto.ReviewThresholdMS != 15_000 {
		t.Fatalf("floor thresholds = %+v", auto)
	}
	missing, err := domain.EvaluateTrackDuration(approvedDurationPolicy(), nil, 100_000)
	if err != nil || missing.Status != domain.DurationManualReview {
		t.Fatalf("missing reference = %+v, %v", missing, err)
	}
}

func TestReleaseDurationBoundariesAreAdditionalAndIndependent(t *testing.T) {
	reference := int64(10_000_000) // auto=100s, review=300s
	for _, test := range []struct {
		difference int64
		want       domain.DurationStatus
	}{
		{99_999, domain.DurationAutoApprove}, {100_000, domain.DurationAutoApprove},
		{100_001, domain.DurationManualReview}, {300_000, domain.DurationManualReview}, {300_001, domain.DurationReject},
	} {
		result, err := domain.EvaluateReleaseDuration(approvedDurationPolicy(), &reference, reference+test.difference)
		if err != nil || result.Status != test.want {
			t.Fatalf("difference %d = %+v, %v", test.difference, result, err)
		}
	}
	if got := domain.StrictestDurationStatus(domain.DurationReject, domain.DurationAutoApprove); got != domain.DurationReject {
		t.Fatalf("total compensated strict track result: %s", got)
	}
}

func TestDurationHandlesMaximumIntegerWithoutOverflow(t *testing.T) {
	reference := int64(math.MaxInt64)
	result, err := domain.EvaluateTrackDuration(approvedDurationPolicy(), &reference, 0)
	if err != nil || result.Status != domain.DurationReject || result.DifferenceMS != math.MaxInt64 {
		t.Fatalf("maximum duration = %+v, %v", result, err)
	}
	if _, err := domain.EvaluateTrackDuration(approvedDurationPolicy(), &reference, -1); err == nil {
		t.Fatal("negative observation accepted")
	}
}

func approvedDurationPolicy() domain.DurationPolicy {
	return domain.DurationPolicy{
		TrackAutoFloorMS: 5_000, TrackAutoPercentBasisPoints: 200,
		TrackManualFloorMS: 15_000, TrackManualPercentBasisPoints: 500,
		ReleaseAutoFloorMS: 30_000, ReleaseAutoPercentBasisPoints: 100,
		ReleaseManualFloorMS: 90_000, ReleaseManualPercentBasisPoints: 300,
	}
}
