package filesystem_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	denyrafs "github.com/waxarsatia/denyra/internal/pipeline/adapters/filesystem"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

func TestUploadWriterKeepsPartialThenRetriesAndFinalizesIdempotently(t *testing.T) {
	t.Parallel()
	uploading := filepath.Join(t.TempDir(), "uploading")
	incoming := filepath.Join(filepath.Dir(uploading), "manual")
	if err := os.MkdirAll(incoming, 0o750); err != nil {
		t.Fatal(err)
	}
	writer := denyrafs.UploadWriter{Root: uploading}
	if err := writer.CreateSession("session-1"); err != nil {
		t.Fatal(err)
	}
	file := domain.UploadFileSpec{ID: "entry-1", RelativePath: "OFF GUARD/01 - Track.flac", SizeBytes: 12}
	if _, err := writer.PutFile(context.Background(), "session-1", file, strings.NewReader("partial")); err == nil {
		t.Fatal("short upload accepted")
	}
	partial := filepath.Join(uploading, "session-1", "OFF GUARD", "01 - Track.flac.partial")
	if info, err := os.Stat(partial); err != nil || info.Size() != 7 {
		t.Fatalf("partial info=%v err=%v", info, err)
	}
	content := "hello world!"
	if written, err := writer.PutFile(context.Background(), "session-1", file, strings.NewReader(content)); err != nil || written != 12 {
		t.Fatalf("retry written=%d err=%v", written, err)
	}
	if written, err := writer.PutFile(context.Background(), "session-1", file, strings.NewReader(content)); err != nil || written != 12 {
		t.Fatalf("idempotent retry written=%d err=%v", written, err)
	}
	if err := writer.VerifySession("session-1", []domain.UploadFileSpec{file}); err != nil {
		t.Fatal(err)
	}
	finalPath, err := writer.FinalizeSession("session-1", incoming, "submission-1", []domain.UploadFileSpec{file})
	if err != nil {
		t.Fatal(err)
	}
	secondPath, err := writer.FinalizeSession("session-1", incoming, "submission-1", []domain.UploadFileSpec{file})
	if err != nil || secondPath != finalPath {
		t.Fatalf("idempotent finalize path=%q err=%v", secondPath, err)
	}
	data, err := os.ReadFile(filepath.Join(finalPath, "OFF GUARD", "01 - Track.flac"))
	if err != nil || string(data) != content {
		t.Fatalf("final content=%q err=%v", data, err)
	}
}

func TestUploadWriterRejectsSymlinkTraversal(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	uploading := filepath.Join(root, "uploading")
	writer := denyrafs.UploadWriter{Root: uploading}
	if err := writer.CreateSession("session-1"); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(uploading, "session-1", "album")); err != nil {
		t.Fatal(err)
	}
	file := domain.UploadFileSpec{ID: "entry-1", RelativePath: "album/track.flac", SizeBytes: 4}
	if _, err := writer.PutFile(context.Background(), "session-1", file, strings.NewReader("data")); err == nil {
		t.Fatal("symlink traversal accepted")
	}
	if _, err := os.Stat(filepath.Join(outside, "track.flac")); !os.IsNotExist(err) {
		t.Fatalf("outside path changed: %v", err)
	}
}

func TestUploadWriterDoesNotAdoptExistingTargetWhileSourceStillExists(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	uploading := filepath.Join(root, "uploading")
	incoming := filepath.Join(root, "manual")
	writer := denyrafs.UploadWriter{Root: uploading}
	file := domain.UploadFileSpec{ID: "entry-1", RelativePath: "track.flac", SizeBytes: 4}
	if err := writer.CreateSession("session-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.PutFile(context.Background(), "session-1", file, strings.NewReader("data")); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(incoming, "submission-1")
	if err := os.MkdirAll(target, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "track.flac"), []byte("data"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.FinalizeSession("session-1", incoming, "submission-1", []domain.UploadFileSpec{file}); err == nil {
		t.Fatal("existing target was adopted while source session still existed")
	}
	if _, err := os.Stat(filepath.Join(uploading, "session-1", "track.flac")); err != nil {
		t.Fatalf("collision changed source: %v", err)
	}
}
