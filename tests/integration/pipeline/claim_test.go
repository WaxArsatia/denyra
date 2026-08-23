package pipeline_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/adapters/filesystem"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
	"golang.org/x/sys/unix"
)

func TestClaimMovesStableCompletedReleaseAtomically(t *testing.T) {
	root := t.TempDir()
	downloads := filepath.Join(root, "downloads")
	release := filepath.Join(downloads, "release-1")
	work := filepath.Join(root, "work")
	locks := filepath.Join(root, "locks")
	if err := os.MkdirAll(release, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(release, "01.flac"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := claimService(work, locks)
	result, err := service.Claim(context.Background(), "candidate-1", application.CompletionEvidence{
		ID: "download-1", Source: domain.SourceSlskd, SourceRoot: downloads, CompletedPath: release, CompletedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.WorkPath != filepath.Join(work, "candidate-1") || len(result.Entries) != 1 {
		t.Fatalf("claim result = %+v", result)
	}
	if _, err := os.Stat(release); !os.IsNotExist(err) {
		t.Fatalf("source remains after claim: %v", err)
	}
	if _, err := os.Stat(filepath.Join(result.WorkPath, "01.flac")); err != nil {
		t.Fatalf("work file missing: %v", err)
	}
}

func TestClaimManualFingerprintChangeRequiresExplicitResubmit(t *testing.T) {
	root := t.TempDir()
	incoming := filepath.Join(root, "incoming")
	release := filepath.Join(incoming, "submission-1")
	if err := os.MkdirAll(release, 0o750); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(release, "01.flac")
	if err := os.WriteFile(file, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	sealed, err := filesystem.Scan(release)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("after"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := claimService(filepath.Join(root, "work"), filepath.Join(root, "locks"))
	_, err = service.Claim(context.Background(), "candidate-1", application.CompletionEvidence{
		ID: "submission-1", Source: domain.SourceManual, SourceRoot: incoming, CompletedPath: release,
		CompletedAt: time.Now().UTC(), SealedFingerprint: sealed.Fingerprint,
	})
	if !errors.Is(err, application.ErrWaitingResubmit) {
		t.Fatalf("manual drift error = %v", err)
	}
	if _, statErr := os.Stat(release); statErr != nil {
		t.Fatalf("manual source moved after drift: %v", statErr)
	}
}

func TestClaimRejectsChangingTreeAndUnsafeEntries(t *testing.T) {
	t.Run("changing file", func(t *testing.T) {
		root := t.TempDir()
		downloads := filepath.Join(root, "downloads")
		release := filepath.Join(downloads, "release")
		if err := os.MkdirAll(release, 0o750); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(release, "track.flac")
		if err := os.WriteFile(file, []byte("one"), 0o600); err != nil {
			t.Fatal(err)
		}
		service := claimService(filepath.Join(root, "work"), filepath.Join(root, "locks"))
		service.Pause = func(context.Context, time.Duration) error { return os.WriteFile(file, []byte("two-two"), 0o600) }
		_, err := service.Claim(context.Background(), "candidate-1", application.CompletionEvidence{ID: "download", Source: domain.SourceOther, SourceRoot: downloads, CompletedPath: release, CompletedAt: time.Now().UTC()})
		if !errors.Is(err, application.ErrUnstableRelease) {
			t.Fatalf("changing tree error = %v", err)
		}
	})

	for name, create := range map[string]func(string) error{
		"symlink": func(path string) error { return os.Symlink("target", path) },
		"fifo":    func(path string) error { return unix.Mkfifo(path, 0o600) },
	} {
		t.Run(name, func(t *testing.T) {
			release := filepath.Join(t.TempDir(), "release")
			if err := os.MkdirAll(release, 0o750); err != nil {
				t.Fatal(err)
			}
			if err := create(filepath.Join(release, "unsafe")); err != nil {
				t.Fatal(err)
			}
			if _, err := filesystem.Scan(release); !errors.Is(err, filesystem.ErrUnsafeTree) {
				t.Fatalf("unsafe %s error = %v", name, err)
			}
		})
	}
}

func TestClaimLockContentionAndOutsideEvidencePath(t *testing.T) {
	root := t.TempDir()
	locks := filepath.Join(root, "locks")
	if err := os.MkdirAll(locks, 0o700); err != nil {
		t.Fatal(err)
	}
	lock, err := filesystem.AcquireLock(filepath.Join(locks, "candidate-1.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	downloads := filepath.Join(root, "downloads")
	release := filepath.Join(downloads, "release")
	if err := os.MkdirAll(release, 0o750); err != nil {
		t.Fatal(err)
	}
	service := claimService(filepath.Join(root, "work"), locks)
	_, err = service.Claim(context.Background(), "candidate-1", application.CompletionEvidence{ID: "download", Source: domain.SourceSlskd, SourceRoot: downloads, CompletedPath: release, CompletedAt: time.Now().UTC()})
	if !errors.Is(err, filesystem.ErrLockHeld) {
		t.Fatalf("lock contention error = %v", err)
	}
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(outside, 0o750); err != nil {
		t.Fatal(err)
	}
	_, err = service.Claim(context.Background(), "candidate-2", application.CompletionEvidence{ID: "download-2", Source: domain.SourceSlskd, SourceRoot: downloads, CompletedPath: outside, CompletedAt: time.Now().UTC()})
	if err == nil {
		t.Fatal("outside evidence path accepted")
	}
}

func TestClaimDetectsIdentityChangeAtMoveBoundary(t *testing.T) {
	root := t.TempDir()
	downloads := filepath.Join(root, "downloads")
	release := filepath.Join(downloads, "release")
	if err := os.MkdirAll(release, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(release, "track.flac"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := claimService(filepath.Join(root, "work"), filepath.Join(root, "locks"))
	service.Move = func(source, target string) error {
		if err := filesystem.MoveAtomic(source, target); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(target, "track.flac"), []byte("swapped"), 0o600)
	}
	_, err := service.Claim(context.Background(), "candidate-1", application.CompletionEvidence{ID: "download", Source: domain.SourceSlskd, SourceRoot: downloads, CompletedPath: release, CompletedAt: time.Now().UTC()})
	if !errors.Is(err, application.ErrUnstableRelease) {
		t.Fatalf("move-boundary identity error = %v", err)
	}
}

func claimService(work, locks string) application.ClaimService {
	return application.ClaimService{
		WorkRoot: work, LockRoot: locks, StabilityInterval: 10 * time.Second,
		Pause: func(context.Context, time.Duration) error { return nil },
	}
}
