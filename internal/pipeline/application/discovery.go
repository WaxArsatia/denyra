package application

import (
	"context"
	"fmt"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type DiscoveryStore interface {
	DiscoverSubmission(context.Context, string, string, time.Time) error
}
type DiscoveryService struct {
	Store        DiscoveryStore
	IncomingRoot string
	Now          func() time.Time
}

func (s DiscoveryService) Scan(ctx context.Context) (int, error) {
	if s.Store == nil || s.IncomingRoot == "" {
		return 0, fmt.Errorf("discovery service is not configured")
	}
	entries, err := os.ReadDir(s.IncomingRoot)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	count := 0
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() || domain.ValidateCandidateID(entry.Name()) != nil {
			continue
		}
		path := filepath.Join(s.IncomingRoot, entry.Name())
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if err := s.Store.DiscoverSubmission(ctx, entry.Name(), path, now); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}
