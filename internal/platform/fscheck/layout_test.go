package fscheck_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/waxarsatia/denyra/internal/platform/fscheck"
)

func TestCheckAcceptsSameDeviceOwnedLayout(t *testing.T) {
	t.Parallel()
	layout := makeLayout(t, false)
	report, err := fscheck.Check(layout)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(report.Paths) != 8 {
		t.Fatalf("report paths = %d, want 8", len(report.Paths))
	}
	for _, path := range report.Paths {
		if path.Canonical == "" || path.DeviceID == 0 {
			t.Fatalf("incomplete report: %+v", path)
		}
	}
}

func TestCheckRejectsSymlink(t *testing.T) {
	t.Parallel()
	layout := makeLayout(t, false)
	realPath := layout.DownloadsSlskd
	symlink := filepath.Join(filepath.Dir(realPath), "slskd-link")
	if err := os.Symlink(realPath, symlink); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	layout.DownloadsSlskd = symlink
	if _, err := fscheck.Check(layout); err == nil {
		t.Fatal("Check accepted symlink")
	}
}

func TestCheckRejectsWrongOwnerOrWritableLibrary(t *testing.T) {
	t.Parallel()
	layout := makeLayout(t, true)
	layout.ExpectedUID++
	if _, err := fscheck.Check(layout); err == nil {
		t.Fatal("Check accepted wrong owner")
	}

	layout = makeLayout(t, true)
	if err := os.Chmod(layout.Library, 0o755); err != nil {
		t.Fatalf("chmod library: %v", err)
	}
	if _, err := fscheck.Check(layout); err == nil {
		t.Fatal("Check accepted writable pipeline library")
	}
}

func TestCheckRejectsDownloaderOutsideDataRoot(t *testing.T) {
	t.Parallel()
	layout := makeLayout(t, false)
	layout.DownloadsOther = t.TempDir()
	if _, err := fscheck.Check(layout); err == nil {
		t.Fatal("Check accepted downloader path outside data root")
	}
}

func TestCheckDirectoriesValidatesGatewaySubset(t *testing.T) {
	root := t.TempDir()
	downloads := filepath.Join(root, "downloads", "spotiflac")
	state := filepath.Join(root, "state", "gateway")
	for _, path := range []string{downloads, state} {
		if err := os.MkdirAll(path, 0o750); err != nil {
			t.Fatalf("mkdir subset: %v", err)
		}
	}
	report, err := fscheck.CheckDirectories(root, []fscheck.Directory{{Name: "downloads", Path: downloads}, {Name: "state", Path: state}}, os.Getuid(), os.Getgid(), 0o700)
	if err != nil {
		t.Fatalf("CheckDirectories: %v", err)
	}
	if len(report.Paths) != 2 {
		t.Fatalf("subset report paths = %d", len(report.Paths))
	}
	if _, err := fscheck.CheckDirectories(root, []fscheck.Directory{{Name: "outside", Path: t.TempDir()}}, os.Getuid(), os.Getgid(), 0o700); err == nil {
		t.Fatal("CheckDirectories accepted path outside data root")
	}
}

func makeLayout(t *testing.T, libraryReadOnly bool) fscheck.Layout {
	t.Helper()
	root := t.TempDir()
	paths := map[string]string{
		"slskd": "downloads/slskd", "spotiflac": "downloads/spotiflac", "other": "downloads/other",
		"incoming": "incoming/manual", "work": "processing/work", "approved": "processing/approved",
		"quarantine": "quarantine", "library": "library",
	}
	for _, relative := range paths {
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(path, 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
	}
	if libraryReadOnly {
		if err := os.Chmod(filepath.Join(root, paths["library"]), 0o550); err != nil {
			t.Fatalf("chmod library: %v", err)
		}
	}
	return fscheck.Layout{
		DataRoot: root, DownloadsSlskd: filepath.Join(root, paths["slskd"]), DownloadsSpotiFLAC: filepath.Join(root, paths["spotiflac"]),
		DownloadsOther: filepath.Join(root, paths["other"]), IncomingManual: filepath.Join(root, paths["incoming"]), Work: filepath.Join(root, paths["work"]),
		Approved: filepath.Join(root, paths["approved"]), Quarantine: filepath.Join(root, paths["quarantine"]), Library: filepath.Join(root, paths["library"]),
		ExpectedUID: os.Getuid(), ExpectedGID: os.Getgid(), MinimumMode: 0o500, RequireLibraryReadOnly: libraryReadOnly,
	}
}
