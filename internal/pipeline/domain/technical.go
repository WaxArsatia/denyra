package domain

import "time"

type TechnicalInfo struct {
	Container  string `json:"container"`
	Codec      string `json:"codec"`
	Channels   int    `json:"channels"`
	DurationMS int64  `json:"duration_ms"`
	SampleRate int    `json:"sample_rate"`
	BitDepth   int    `json:"bit_depth,omitempty"`
}

func (i TechnicalInfo) ValidFLAC() bool {
	return i.Container == "flac" && i.Codec == "flac" && i.Channels > 0 && i.DurationMS > 0 && i.SampleRate > 0 && i.BitDepth >= 0
}

type CommandEvidence struct {
	Tool       string        `json:"tool"`
	Version    string        `json:"version"`
	Arguments  []string      `json:"arguments"`
	ExitStatus int           `json:"exit_status"`
	Stdout     string        `json:"stdout,omitempty"`
	Stderr     string        `json:"stderr,omitempty"`
	Duration   time.Duration `json:"duration"`
	TimedOut   bool          `json:"timed_out"`
	Truncated  bool          `json:"truncated"`
}

type FileTechnicalEvidence struct {
	RelativePath     string              `json:"relative_path"`
	SHA256Before     string              `json:"sha256_before"`
	Info             TechnicalInfo       `json:"info"`
	OriginalComments map[string][]string `json:"original_comments"`
	EmbeddedPictures int                 `json:"embedded_pictures"`
	Commands         []CommandEvidence   `json:"commands"`
}

type TechnicalReleaseResult struct {
	Files     []FileTechnicalEvidence `json:"files"`
	Warnings  []Warning               `json:"warnings"`
	Rejected  bool                    `json:"rejected"`
	Retryable bool                    `json:"retryable"`
	Reason    string                  `json:"reason,omitempty"`
}
