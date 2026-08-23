package media

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

type MetaFLAC struct {
	Binary  string
	Version string
	Timeout time.Duration
	Runner  Runner
}

func (m MetaFLAC) ReadTags(ctx context.Context, path string) (domain.TagSet, domain.CommandEvidence, error) {
	evidence, err := m.run(ctx, "--export-tags-to=-", path)
	if err != nil {
		return nil, evidence, err
	}
	tags := domain.TagSet{}
	for _, line := range strings.Split(strings.ReplaceAll(evidence.Stdout, "\r\n", "\n"), "\n") {
		if line == "" {
			continue
		}
		field, value, ok := strings.Cut(line, "=")
		if !ok || field == "" {
			return nil, evidence, fmt.Errorf("invalid exported Vorbis comment %q", line)
		}
		field = strings.ToUpper(field)
		tags[field] = append(tags[field], value)
	}
	return tags, evidence, nil
}

func (m MetaFLAC) AudioMD5(ctx context.Context, path string) (string, domain.CommandEvidence, error) {
	evidence, err := m.run(ctx, "--show-md5sum", path)
	if err != nil {
		return "", evidence, err
	}
	value := strings.TrimSpace(evidence.Stdout)
	if len(value) != 32 {
		return "", evidence, fmt.Errorf("invalid FLAC STREAMINFO MD5 %q", value)
	}
	return value, evidence, nil
}

func (m MetaFLAC) PictureCount(ctx context.Context, path string) (int, domain.CommandEvidence, error) {
	evidence, err := m.run(ctx, "--list", "--block-type=PICTURE", path)
	if err != nil {
		return 0, evidence, err
	}
	return strings.Count(evidence.Stdout, "type: 6 (PICTURE)"), evidence, nil
}

func (m MetaFLAC) Apply(ctx context.Context, path string, tags domain.TagSet, removePictures bool) ([]domain.CommandEvidence, error) {
	entries, err := tags.OrderedEntries()
	if err != nil {
		return nil, err
	}
	arguments := []string{"--preserve-modtime"}
	for _, field := range domain.ManagedTagFields {
		arguments = append(arguments, "--remove-tag="+field)
	}
	arguments = append(arguments, "--remove-tag=MUSICBRAINZ_RECORDINGID")
	for _, entry := range entries {
		arguments = append(arguments, "--set-tag="+entry)
	}
	arguments = append(arguments, path)
	tagEvidence, err := m.run(ctx, arguments...)
	result := []domain.CommandEvidence{tagEvidence}
	if err != nil {
		return result, err
	}
	if removePictures {
		pictureEvidence, err := m.run(ctx, "--preserve-modtime", "--remove", "--block-type=PICTURE", path)
		result = append(result, pictureEvidence)
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

func (m MetaFLAC) run(ctx context.Context, arguments ...string) (domain.CommandEvidence, error) {
	timeout := m.Timeout
	if timeout <= 0 {
		timeout = time.Minute
	}
	child, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return m.Runner.Run(child, m.Binary, m.Version, arguments...)
}
