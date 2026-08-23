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
	IdentityRank        int `json:"identity_rank"`
	EditionRank         int `json:"edition_rank"`
	QualityWarningCount int `json:"quality_warning_count"`
	SourceConfidence    int `json:"source_confidence"`
	BitDepth            int `json:"bit_depth"`
	SampleRate          int `json:"sample_rate"`
}

type CallbackResult struct {
	StatusCode     int    `json:"status_code"`
	ResponseSHA256 string `json:"response_sha256"`
}
