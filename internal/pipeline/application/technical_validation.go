package application

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

type MediaInspector interface {
	Inspect(context.Context, string) (domain.TechnicalInfo, map[string][]string, domain.CommandEvidence, error)
}

type IntegrityTester interface {
	Test(context.Context, string) (domain.CommandEvidence, error)
}

type LosslessHeuristic interface {
	Analyze(context.Context, string, domain.TechnicalInfo) ([]domain.Warning, error)
}

type TechnicalValidator struct {
	Inspector MediaInspector
	Integrity IntegrityTester
	Heuristic LosslessHeuristic
	Checksum  func(string) (string, error)
}

func (v TechnicalValidator) Validate(ctx context.Context, workRoot string, relativePaths []string) domain.TechnicalReleaseResult {
	result := domain.TechnicalReleaseResult{}
	paths := append([]string(nil), relativePaths...)
	sort.Strings(paths)
	for _, relative := range paths {
		extension := strings.ToLower(filepath.Ext(relative))
		if extension != ".flac" {
			if isAudioExtension(extension) {
				result.Rejected, result.Reason = true, fmt.Sprintf("non-FLAC audio is not accepted: %s", relative)
				return result
			}
			continue
		}
		path := filepath.Join(workRoot, filepath.FromSlash(relative))
		checksum, err := v.Checksum(path)
		if err != nil {
			result.Rejected, result.Reason = true, fmt.Sprintf("checksum %s: %v", relative, err)
			return result
		}
		info, comments, probeEvidence, err := v.Inspector.Inspect(ctx, path)
		fileEvidence := domain.FileTechnicalEvidence{RelativePath: relative, SHA256Before: checksum, Info: info, OriginalComments: comments, Commands: []domain.CommandEvidence{probeEvidence}}
		if err != nil {
			result.Files = append(result.Files, fileEvidence)
			if operationalToolFailure(probeEvidence) {
				result.Retryable, result.Reason = true, fmt.Sprintf("ffprobe operational failure %s: %v", relative, err)
			} else {
				result.Rejected, result.Reason = true, fmt.Sprintf("ffprobe hard gate %s: %v", relative, err)
			}
			return result
		}
		integrityEvidence, err := v.Integrity.Test(ctx, path)
		fileEvidence.Commands = append(fileEvidence.Commands, integrityEvidence)
		result.Files = append(result.Files, fileEvidence)
		if err != nil {
			if operationalToolFailure(integrityEvidence) {
				result.Retryable, result.Reason = true, fmt.Sprintf("flac operational failure %s: %v", relative, err)
			} else {
				result.Rejected, result.Reason = true, fmt.Sprintf("flac integrity hard gate %s: %v", relative, err)
			}
			return result
		}
		if v.Heuristic != nil {
			warnings, err := v.Heuristic.Analyze(ctx, path, info)
			if err != nil {
				result.Warnings = append(result.Warnings, domain.Warning{Kind: domain.WarningQuality, Code: "HEURISTIC_UNAVAILABLE", Details: err.Error()})
			} else {
				for _, warning := range warnings {
					if warning.Kind == domain.WarningNonBlocking {
						warning.Kind = domain.WarningQuality
					}
					result.Warnings = append(result.Warnings, warning)
				}
			}
		}
	}
	if len(result.Files) == 0 {
		result.Rejected, result.Reason = true, "release contains no FLAC tracks"
	}
	return result
}

func operationalToolFailure(evidence domain.CommandEvidence) bool {
	return evidence.TimedOut || evidence.ExitStatus < 0
}

func isAudioExtension(extension string) bool {
	switch extension {
	case ".mp3", ".m4a", ".aac", ".ogg", ".opus", ".wav", ".aiff", ".alac", ".wma":
		return true
	default:
		return false
	}
}
