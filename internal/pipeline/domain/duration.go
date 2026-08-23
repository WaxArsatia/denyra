package domain

import (
	"fmt"
	"math"
)

type DurationStatus string

const (
	DurationAutoApprove  DurationStatus = "AUTO_APPROVE"
	DurationManualReview DurationStatus = "MANUAL_REVIEW"
	DurationReject       DurationStatus = "REJECT"
)

type DurationResult struct {
	ReferenceMS       *int64         `json:"reference_ms,omitempty"`
	ObservedMS        int64          `json:"observed_ms"`
	DifferenceMS      int64          `json:"difference_ms"`
	AutoThresholdMS   int64          `json:"auto_threshold_ms"`
	ReviewThresholdMS int64          `json:"review_threshold_ms"`
	Status            DurationStatus `json:"status"`
}

type DurationPolicy struct {
	TrackAutoFloorMS                int64
	TrackAutoPercentBasisPoints     int64
	TrackManualFloorMS              int64
	TrackManualPercentBasisPoints   int64
	ReleaseAutoFloorMS              int64
	ReleaseAutoPercentBasisPoints   int64
	ReleaseManualFloorMS            int64
	ReleaseManualPercentBasisPoints int64
}

func (p DurationPolicy) Validate() error {
	values := []int64{p.TrackAutoFloorMS, p.TrackAutoPercentBasisPoints, p.TrackManualFloorMS, p.TrackManualPercentBasisPoints,
		p.ReleaseAutoFloorMS, p.ReleaseAutoPercentBasisPoints, p.ReleaseManualFloorMS, p.ReleaseManualPercentBasisPoints}
	for _, value := range values {
		if value <= 0 {
			return fmt.Errorf("duration policy values must be positive")
		}
	}
	if p.TrackManualFloorMS < p.TrackAutoFloorMS || p.TrackManualPercentBasisPoints < p.TrackAutoPercentBasisPoints ||
		p.ReleaseManualFloorMS < p.ReleaseAutoFloorMS || p.ReleaseManualPercentBasisPoints < p.ReleaseAutoPercentBasisPoints {
		return fmt.Errorf("manual duration thresholds cannot be lower than auto thresholds")
	}
	return nil
}

func EvaluateTrackDuration(policy DurationPolicy, referenceMS *int64, observedMS int64) (DurationResult, error) {
	if err := policy.Validate(); err != nil {
		return DurationResult{}, err
	}
	return evaluateDuration(referenceMS, observedMS, policy.TrackAutoFloorMS, policy.TrackAutoPercentBasisPoints, policy.TrackManualFloorMS, policy.TrackManualPercentBasisPoints)
}

func EvaluateReleaseDuration(policy DurationPolicy, referenceMS *int64, observedMS int64) (DurationResult, error) {
	if err := policy.Validate(); err != nil {
		return DurationResult{}, err
	}
	return evaluateDuration(referenceMS, observedMS, policy.ReleaseAutoFloorMS, policy.ReleaseAutoPercentBasisPoints, policy.ReleaseManualFloorMS, policy.ReleaseManualPercentBasisPoints)
}

func evaluateDuration(referenceMS *int64, observedMS, autoFloorMS, autoBasisPoints, reviewFloorMS, reviewBasisPoints int64) (DurationResult, error) {
	if observedMS < 0 {
		return DurationResult{}, fmt.Errorf("observed duration cannot be negative")
	}
	result := DurationResult{ReferenceMS: referenceMS, ObservedMS: observedMS}
	if referenceMS == nil || *referenceMS <= 0 {
		result.Status = DurationManualReview
		return result, nil
	}
	autoThreshold, err := thresholdMS(*referenceMS, autoFloorMS, autoBasisPoints)
	if err != nil {
		return DurationResult{}, err
	}
	reviewThreshold, err := thresholdMS(*referenceMS, reviewFloorMS, reviewBasisPoints)
	if err != nil {
		return DurationResult{}, err
	}
	difference := absoluteDifference(*referenceMS, observedMS)
	result.DifferenceMS, result.AutoThresholdMS, result.ReviewThresholdMS = difference, autoThreshold, reviewThreshold
	switch {
	case difference <= autoThreshold:
		result.Status = DurationAutoApprove
	case difference <= reviewThreshold:
		result.Status = DurationManualReview
	default:
		result.Status = DurationReject
	}
	return result, nil
}

func thresholdMS(referenceMS, floorMS, percentBasisPoints int64) (int64, error) {
	if referenceMS < 0 || floorMS < 0 || percentBasisPoints < 0 {
		return 0, fmt.Errorf("duration threshold inputs cannot be negative")
	}
	whole, remainder := referenceMS/10_000, referenceMS%10_000
	if percentBasisPoints != 0 && whole > math.MaxInt64/percentBasisPoints {
		return 0, fmt.Errorf("duration threshold overflow")
	}
	percent := whole * percentBasisPoints
	if remainder != 0 && percentBasisPoints > math.MaxInt64/remainder {
		return 0, fmt.Errorf("duration threshold overflow")
	}
	remainderProduct := remainder * percentBasisPoints
	remainderCeil := remainderProduct / 10_000
	if remainderProduct%10_000 != 0 {
		remainderCeil++
	}
	if percent > math.MaxInt64-remainderCeil {
		return 0, fmt.Errorf("duration threshold overflow")
	}
	percent += remainderCeil
	if percent > floorMS {
		return percent, nil
	}
	return floorMS, nil
}

func absoluteDifference(left, right int64) int64 {
	if left >= right {
		return left - right
	}
	return right - left
}

func StrictestDurationStatus(values ...DurationStatus) DurationStatus {
	strictest := DurationAutoApprove
	for _, value := range values {
		if durationRank(value) > durationRank(strictest) {
			strictest = value
		}
	}
	return strictest
}

func durationRank(value DurationStatus) int {
	switch value {
	case DurationReject:
		return 2
	case DurationManualReview:
		return 1
	default:
		return 0
	}
}
