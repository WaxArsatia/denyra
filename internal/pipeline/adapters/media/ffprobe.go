package media

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

type FFProbe struct {
	Binary  string
	Version string
	Timeout time.Duration
	Runner  Runner
}

func (p FFProbe) Inspect(ctx context.Context, path string) (domain.TechnicalInfo, map[string][]string, domain.CommandEvidence, error) {
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	child, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	evidence, err := p.Runner.Run(child, p.Binary, p.Version, "-v", "error", "-show_format", "-show_streams", "-of", "json", path)
	if err != nil {
		return domain.TechnicalInfo{}, nil, evidence, err
	}
	var payload struct {
		Streams []struct {
			CodecName     string            `json:"codec_name"`
			CodecType     string            `json:"codec_type"`
			Channels      int               `json:"channels"`
			SampleRate    string            `json:"sample_rate"`
			BitsPerSample string            `json:"bits_per_raw_sample"`
			Duration      string            `json:"duration"`
			Tags          map[string]string `json:"tags"`
		} `json:"streams"`
		Format struct {
			FormatName string            `json:"format_name"`
			Duration   string            `json:"duration"`
			Tags       map[string]string `json:"tags"`
		} `json:"format"`
	}
	if err := json.Unmarshal([]byte(evidence.Stdout), &payload); err != nil {
		return domain.TechnicalInfo{}, nil, evidence, fmt.Errorf("decode ffprobe JSON: %w", err)
	}
	var audio *struct {
		CodecName     string            `json:"codec_name"`
		CodecType     string            `json:"codec_type"`
		Channels      int               `json:"channels"`
		SampleRate    string            `json:"sample_rate"`
		BitsPerSample string            `json:"bits_per_raw_sample"`
		Duration      string            `json:"duration"`
		Tags          map[string]string `json:"tags"`
	}
	for index := range payload.Streams {
		if payload.Streams[index].CodecType == "audio" {
			if audio != nil {
				return domain.TechnicalInfo{}, nil, evidence, fmt.Errorf("multiple audio streams are not a valid FLAC candidate")
			}
			audio = &payload.Streams[index]
		}
	}
	if audio == nil {
		return domain.TechnicalInfo{}, nil, evidence, fmt.Errorf("ffprobe found no audio stream")
	}
	sampleRate, err := strconv.Atoi(audio.SampleRate)
	if err != nil {
		return domain.TechnicalInfo{}, nil, evidence, fmt.Errorf("invalid sample rate %q", audio.SampleRate)
	}
	durationText := audio.Duration
	if durationText == "" {
		durationText = payload.Format.Duration
	}
	durationSeconds, err := strconv.ParseFloat(durationText, 64)
	if err != nil || durationSeconds <= 0 {
		return domain.TechnicalInfo{}, nil, evidence, fmt.Errorf("invalid duration %q", durationText)
	}
	bitDepth := 0
	if audio.BitsPerSample != "" {
		bitDepth, err = strconv.Atoi(audio.BitsPerSample)
		if err != nil || bitDepth < 0 {
			return domain.TechnicalInfo{}, nil, evidence, fmt.Errorf("invalid bit depth %q", audio.BitsPerSample)
		}
	}
	info := domain.TechnicalInfo{
		Container: strings.ToLower(payload.Format.FormatName), Codec: strings.ToLower(audio.CodecName), Channels: audio.Channels,
		DurationMS: int64(durationSeconds*1000 + 0.5), SampleRate: sampleRate, BitDepth: bitDepth,
	}
	comments := make(map[string][]string)
	for key, value := range payload.Format.Tags {
		appendComment(comments, key, value)
	}
	for key, value := range audio.Tags {
		appendComment(comments, key, value)
	}
	if !info.ValidFLAC() {
		return info, comments, evidence, fmt.Errorf("codec/container or technical structure is not valid FLAC: %+v", info)
	}
	return info, comments, evidence, nil
}

func appendComment(comments map[string][]string, key, value string) {
	key = strings.ToUpper(key)
	switch key {
	case "TRACK":
		key = "TRACKNUMBER"
	case "DISC":
		key = "DISCNUMBER"
	case "ALBUM_ARTIST":
		key = "ALBUMARTIST"
	}
	for _, existing := range comments[key] {
		if existing == value {
			return
		}
	}
	comments[key] = append(comments[key], value)
}
