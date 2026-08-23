package integration_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/waxarsatia/denyra/internal/platform/fscheck"
)

func TestFilesystemDeviceComparisonDetectsKnownCrossDevicePath(t *testing.T) {
	first := t.TempDir()
	secondRoot := "/dev/shm"
	if _, err := os.Stat(secondRoot); err != nil {
		t.Skip("/dev/shm unavailable")
	}
	second, err := os.MkdirTemp(secondRoot, "denyra-device-test-")
	if err != nil {
		t.Skipf("cannot create cross-device fixture: %v", err)
	}
	defer os.RemoveAll(second)
	same, err := fscheck.SameDevice(filepath.Clean(first), filepath.Clean(second))
	if err != nil {
		t.Fatalf("SameDevice: %v", err)
	}
	if same {
		t.Skip("test and /dev/shm are on the same device")
	}
}
