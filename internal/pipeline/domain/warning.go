package domain

type WarningKind string

const (
	WarningQuality     WarningKind = "QUALITY_WARNING"
	WarningNonBlocking WarningKind = "NON_BLOCKING_WARNING"
)

type Warning struct {
	Kind    WarningKind `json:"kind"`
	Code    string      `json:"code"`
	Details string      `json:"details"`
}
