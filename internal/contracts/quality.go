package contracts

type WarningClass string

const (
	QualityWarning     WarningClass = "QUALITY_WARNING"
	NonBlockingWarning WarningClass = "NON_BLOCKING_WARNING"
)

type Warning struct {
	Class   WarningClass `json:"class"`
	Code    string       `json:"code"`
	Message string       `json:"message"`
}

type QualityVector struct {
	ReleaseCorrect      bool `json:"release_correct"`
	SourceConfidence    int  `json:"source_confidence"`
	BitDepth            int  `json:"bit_depth"`
	SampleRate          int  `json:"sample_rate"`
	QualityWarningCount int  `json:"quality_warning_count"`
}
