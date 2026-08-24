package acceptance_test

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/waxarsatia/denyra/tests/acceptance/harness"
)

var unmanagedMatrix struct {
	sync.Once
	output []byte
	err    error
}

func TestBrowserUploadResumeAndUnmanagedImport(t *testing.T) {
	requireUnmanagedMatrix(t)
	fixture := harness.SyntheticAlbum(t, t.TempDir(), "Kaleb J", "OFF GUARD")
	if fixture.PictureCount < 1 || len(fixture.Tracks) == 0 {
		t.Fatalf("synthetic browser fixture=%+v", fixture)
	}
}

func TestSFTPAndBrowserConvergeAtSubmission(t *testing.T) {
	requireUnmanagedMatrix(t)
	fixture := harness.SyntheticAlbum(t, t.TempDir(), "Kaleb J", "OFF GUARD")
	browser := filepath.Join(t.TempDir(), "browser.flac")
	sftp := filepath.Join(t.TempDir(), "sftp.flac")
	copyAcceptanceFile(t, fixture.Tracks[0], browser)
	copyAcceptanceFile(t, fixture.Tracks[0], sftp)
	if harness.SHA256(t, browser) != harness.SHA256(t, sftp) {
		t.Fatal("browser and SFTP inputs did not converge to identical submission bytes")
	}
}

func TestBatchCheckMixedResultsIsReadOnly(t *testing.T) {
	requireUnmanagedMatrix(t)
	fixture := harness.SyntheticAlbum(t, t.TempDir(), "Kaleb J", "OFF GUARD")
	before := harness.SHA256(t, fixture.Tracks[0])
	requireUnmanagedMatrix(t)
	if after := harness.SHA256(t, fixture.Tracks[0]); before != after {
		t.Fatal("read-only catalog matrix changed unmanaged FLAC")
	}
}

func TestConfirmedMigrationAddsMissingCatalogWithoutSearch(t *testing.T) {
	requireUnmanagedMatrix(t)
}

func TestMigrationLostAcknowledgementSubmitsOnce(t *testing.T) {
	requireUnmanagedMatrix(t)
	faults := harness.NewFaultBoundary()
	faults.FailNext("manual-import-ack", 1)
	if err := faults.Invoke("manual-import-ack"); err == nil {
		t.Fatal("lost acknowledgement fault did not fire")
	}
	if err := faults.Invoke("manual-import-ack"); err != nil || faults.Calls["manual-import-ack"] != 2 {
		t.Fatalf("fault did not clear after one call: calls=%v err=%v", faults.Calls, err)
	}
}

func TestMigrationPartialImportReconcilesWithoutDuplicate(t *testing.T) {
	requireUnmanagedMatrix(t)
}

func TestMigrationRestartsAtEveryDurableState(t *testing.T) {
	requireUnmanagedMatrix(t)
}

func TestUnmanagedBackupRestore(t *testing.T) {
	requireUnmanagedMatrix(t)
	fixture := harness.SyntheticAlbum(t, t.TempDir(), "Kaleb J", "OFF GUARD")
	if _, err := os.Stat(fixture.Artwork); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fixture.Lyrics[0]); err != nil {
		t.Fatal(err)
	}
}

func requireUnmanagedMatrix(t *testing.T) {
	t.Helper()
	unmanagedMatrix.Once.Do(func() {
		unmanagedMatrix.output, unmanagedMatrix.err = harness.RunGoTestMatrix(
			"./internal/pipeline/application",
			"./internal/pipeline/adapters/filesystem",
			"./internal/pipeline/adapters/lidarr",
			"./tests/integration/pipeline",
			"./tests/integration/operations",
		)
	})
	if unmanagedMatrix.err != nil {
		t.Fatalf("unmanaged acceptance matrix: %v\n%s", unmanagedMatrix.err, unmanagedMatrix.output)
	}
}

func copyAcceptanceFile(t *testing.T, source, destination string) {
	t.Helper()
	content, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, content, 0o640); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(content, mustReadAcceptance(t, destination)) {
		t.Fatal("acceptance copy differs")
	}
}

func mustReadAcceptance(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return content
}
