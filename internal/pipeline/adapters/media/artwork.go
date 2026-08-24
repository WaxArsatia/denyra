package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var ErrArtworkNotFound = errors.New("local artwork not found")

type Artwork struct {
	MetaFLAC MetaFLAC
	MaxBytes int64
}

func (a Artwork) Embedded(ctx context.Context, root string) ([]byte, string, error) {
	files, err := flacFiles(root)
	if err != nil {
		return nil, "", err
	}
	tool := a.MetaFLAC
	if a.MaxBytes <= 0 {
		return nil, "", fmt.Errorf("artwork maximum size is required")
	}
	tool.Runner.MaxOutput = int(a.MaxBytes + 1)
	for _, path := range files {
		evidence, runErr := tool.run(ctx, "--export-picture-to=-", path)
		if runErr != nil || evidence.Stdout == "" {
			if ctx.Err() != nil {
				return nil, "", ctx.Err()
			}
			continue
		}
		body := []byte(evidence.Stdout)
		if evidence.Truncated || int64(len(body)) > a.MaxBytes {
			return nil, path, fmt.Errorf("embedded artwork exceeds %d bytes", a.MaxBytes)
		}
		return body, path, nil
	}
	return nil, "", ErrArtworkNotFound
}

func (a Artwork) Sidecar(root string) ([]byte, string, error) {
	if a.MaxBytes <= 0 {
		return nil, "", fmt.Errorf("artwork maximum size is required")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, "", err
	}
	var candidates []string
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			continue
		}
		name := strings.ToLower(entry.Name())
		extension := strings.ToLower(filepath.Ext(name))
		base := strings.TrimSuffix(name, extension)
		if (base == "cover" || base == "folder") && (extension == ".jpg" || extension == ".jpeg" || extension == ".png") {
			candidates = append(candidates, entry.Name())
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		left, right := strings.ToLower(candidates[i]), strings.ToLower(candidates[j])
		leftCover, rightCover := strings.HasPrefix(left, "cover."), strings.HasPrefix(right, "cover.")
		if leftCover != rightCover {
			return leftCover
		}
		return left < right
	})
	if len(candidates) == 0 {
		return nil, "", ErrArtworkNotFound
	}
	path := filepath.Join(root, candidates[0])
	file, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, a.MaxBytes+1))
	if err != nil {
		return nil, path, err
	}
	if int64(len(body)) > a.MaxBytes {
		return nil, path, fmt.Errorf("sidecar artwork exceeds %d bytes", a.MaxBytes)
	}
	return body, path, nil
}

func flacFiles(root string) ([]string, error) {
	var result []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type().IsRegular() && strings.EqualFold(filepath.Ext(entry.Name()), ".flac") {
			result = append(result, path)
		}
		return nil
	})
	sort.Strings(result)
	return result, err
}
