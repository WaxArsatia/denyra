// Package deplock validates the immutable external dependency lock.
package deplock

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

type Lock struct {
	Schema     int         `json:"schema"`
	Images     []Image     `json:"images"`
	Artifacts  []Artifact  `json:"artifacts"`
	Registries []Registry  `json:"registries"`
	Components []Component `json:"components"`
}

type Image struct {
	ID        string `json:"id"`
	Reference string `json:"reference"`
	Platform  string `json:"platform"`
	Version   string `json:"version"`
	Digest    string `json:"digest"`
}

type Artifact struct {
	ID       string `json:"id"`
	Version  string `json:"version"`
	Filename string `json:"filename"`
	URL      string `json:"url,omitempty"`
	SHA256   string `json:"sha256"`
}

type Registry struct {
	ID         string `json:"id"`
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
}

type Component struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Source  string `json:"source,omitempty"`
}

func Decode(data []byte) (Lock, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var lock Lock
	if err := decoder.Decode(&lock); err != nil {
		return Lock{}, fmt.Errorf("decode dependency lock: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Lock{}, err
	}
	if err := lock.Validate(); err != nil {
		return Lock{}, err
	}
	return lock, nil
}

func (l Lock) Image(id string) (Image, error) {
	for _, image := range l.Images {
		if image.ID == id {
			return image, nil
		}
	}
	return Image{}, fmt.Errorf("dependency lock has no image %q", id)
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("decode dependency lock: multiple JSON values")
		}
		return fmt.Errorf("decode dependency lock trailer: %w", err)
	}
	return nil
}

func (l Lock) CanonicalJSON() ([]byte, error) {
	clone := l
	clone.Images = append([]Image(nil), l.Images...)
	clone.Artifacts = append([]Artifact(nil), l.Artifacts...)
	clone.Registries = append([]Registry(nil), l.Registries...)
	clone.Components = append([]Component(nil), l.Components...)
	sort.Slice(clone.Images, func(i, j int) bool { return clone.Images[i].ID < clone.Images[j].ID })
	sort.Slice(clone.Artifacts, func(i, j int) bool { return clone.Artifacts[i].ID < clone.Artifacts[j].ID })
	sort.Slice(clone.Registries, func(i, j int) bool { return clone.Registries[i].ID < clone.Registries[j].ID })
	sort.Slice(clone.Components, func(i, j int) bool { return clone.Components[i].ID < clone.Components[j].ID })
	encoded, err := json.MarshalIndent(clone, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode canonical dependency lock: %w", err)
	}
	return append(encoded, '\n'), nil
}
