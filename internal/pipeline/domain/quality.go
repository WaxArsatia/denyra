package domain

type QualityVector struct {
	IdentityRank        int `json:"identity_rank"`
	EditionRank         int `json:"edition_rank"`
	QualityWarningCount int `json:"quality_warning_count"`
	SourceConfidence    int `json:"source_confidence"`
	BitDepth            int `json:"bit_depth"`
	SampleRate          int `json:"sample_rate"`
}

func CompareQuality(left, right QualityVector) int {
	leftValues := []int{left.IdentityRank, left.EditionRank, -left.QualityWarningCount, left.SourceConfidence, left.BitDepth, left.SampleRate}
	rightValues := []int{right.IdentityRank, right.EditionRank, -right.QualityWarningCount, right.SourceConfidence, right.BitDepth, right.SampleRate}
	for index := range leftValues {
		if leftValues[index] > rightValues[index] {
			return 1
		}
		if leftValues[index] < rightValues[index] {
			return -1
		}
	}
	return 0
}

func CountQualityWarnings(warnings []Warning) int {
	count := 0
	for _, warning := range warnings {
		if warning.Kind == WarningQuality {
			count++
		}
	}
	return count
}
