package filesystem

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/waxarsatia/denyra/internal/pipeline/domain"
	"golang.org/x/sys/unix"
)

type UploadWriter struct {
	Root string
}

func (w UploadWriter) CreateSession(sessionID string) error {
	if err := validateUploadRootAndID(w.Root, sessionID); err != nil {
		return err
	}
	if err := os.MkdirAll(w.Root, 0o750); err != nil {
		return fmt.Errorf("create upload root: %w", err)
	}
	rootFD, err := openAbsoluteDirectoryNoFollow(w.Root)
	if err != nil {
		return fmt.Errorf("open upload root: %w", err)
	}
	defer unix.Close(rootFD)
	if err := unix.Mkdirat(rootFD, sessionID, 0o750); err != nil && !errors.Is(err, unix.EEXIST) {
		return fmt.Errorf("create upload session: %w", err)
	}
	sessionFD, err := unix.Openat(rootFD, sessionID, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("open upload session: %w", err)
	}
	return unix.Close(sessionFD)
}

func (w UploadWriter) PutFile(ctx context.Context, sessionID string, spec domain.UploadFileSpec, reader io.Reader) (int64, error) {
	if err := validateUploadRootAndID(w.Root, sessionID); err != nil {
		return 0, err
	}
	if _, err := domain.UploadPathKey(spec.RelativePath); err != nil || spec.SizeBytes < 0 {
		return 0, fmt.Errorf("invalid upload entry %q", spec.RelativePath)
	}
	rootFD, err := openAbsoluteDirectoryNoFollow(w.Root)
	if err != nil {
		return 0, err
	}
	defer unix.Close(rootFD)
	sessionFD, err := unix.Openat(rootFD, sessionID, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return 0, fmt.Errorf("open upload session: %w", err)
	}
	defer unix.Close(sessionFD)
	directoryFD, name, err := openUploadParent(sessionFD, spec.RelativePath)
	if err != nil {
		return 0, err
	}
	defer unix.Close(directoryFD)

	if finalFD, openErr := unix.Openat(directoryFD, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0); openErr == nil {
		defer unix.Close(finalFD)
		matching, compareErr := sameReaderAndFile(ctx, reader, finalFD, spec.SizeBytes)
		if compareErr != nil {
			return 0, compareErr
		}
		if !matching {
			return 0, fmt.Errorf("completed upload entry %q differs from retry", spec.RelativePath)
		}
		return spec.SizeBytes, nil
	} else if !errors.Is(openErr, unix.ENOENT) {
		return 0, fmt.Errorf("inspect completed upload entry: %w", openErr)
	}

	partialName := name + ".partial"
	partialFD, err := unix.Openat(directoryFD, partialName, unix.O_WRONLY|unix.O_CREAT|unix.O_TRUNC|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o640)
	if err != nil {
		return 0, fmt.Errorf("open partial upload: %w", err)
	}
	file := os.NewFile(uintptr(partialFD), partialName)
	written, copyErr := io.Copy(file, io.LimitReader(contextReader{ctx: ctx, reader: reader}, spec.SizeBytes+1))
	if copyErr == nil {
		copyErr = file.Sync()
	}
	closeErr := file.Close()
	if copyErr != nil {
		return written, fmt.Errorf("write partial upload: %w", copyErr)
	}
	if closeErr != nil {
		return written, fmt.Errorf("close partial upload: %w", closeErr)
	}
	if written != spec.SizeBytes {
		return written, fmt.Errorf("upload entry %q wrote %d bytes, expected %d", spec.RelativePath, written, spec.SizeBytes)
	}
	if err := unix.Renameat2(directoryFD, partialName, directoryFD, name, unix.RENAME_NOREPLACE); err != nil {
		if !errors.Is(err, unix.EEXIST) {
			return written, fmt.Errorf("complete upload entry: %w", err)
		}
		matching, compareErr := sameFilesAt(directoryFD, partialName, name, spec.SizeBytes)
		if compareErr != nil || !matching {
			if compareErr != nil {
				return written, compareErr
			}
			return written, fmt.Errorf("completed upload entry %q conflicts", spec.RelativePath)
		}
		if err := unix.Unlinkat(directoryFD, partialName, 0); err != nil {
			return written, fmt.Errorf("remove duplicate partial upload: %w", err)
		}
	}
	if err := unix.Fsync(directoryFD); err != nil {
		return written, fmt.Errorf("sync upload directory: %w", err)
	}
	return written, nil
}

func (w UploadWriter) VerifySession(sessionID string, files []domain.UploadFileSpec) error {
	if err := validateUploadRootAndID(w.Root, sessionID); err != nil {
		return err
	}
	return verifyUploadTree(filepath.Join(w.Root, sessionID), files)
}

func (w UploadWriter) FinalizeSession(sessionID, incomingRoot, submissionID string, files []domain.UploadFileSpec) (string, error) {
	if err := validateUploadRootAndID(w.Root, sessionID); err != nil {
		return "", err
	}
	if err := domain.ValidateCandidateID(submissionID); err != nil {
		return "", err
	}
	if !filepath.IsAbs(incomingRoot) || filepath.Clean(incomingRoot) != incomingRoot {
		return "", fmt.Errorf("incoming root must be absolute and canonical")
	}
	target := filepath.Join(incomingRoot, submissionID)
	sourceExists := false
	if _, err := os.Lstat(filepath.Join(w.Root, sessionID)); err == nil {
		sourceExists = true
		if err := w.VerifySession(sessionID, files); err != nil {
			return "", err
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(incomingRoot, 0o750); err != nil {
		return "", err
	}
	sourceRootFD, err := openAbsoluteDirectoryNoFollow(w.Root)
	if err != nil {
		return "", err
	}
	defer unix.Close(sourceRootFD)
	targetRootFD, err := openAbsoluteDirectoryNoFollow(incomingRoot)
	if err != nil {
		return "", err
	}
	defer unix.Close(targetRootFD)
	if err := unix.Renameat2(sourceRootFD, sessionID, targetRootFD, submissionID, unix.RENAME_NOREPLACE); err != nil {
		if !errors.Is(err, unix.EEXIST) && !errors.Is(err, unix.ENOENT) {
			if errors.Is(err, unix.EXDEV) {
				return "", ErrCrossDevice
			}
			return "", fmt.Errorf("finalize upload session: %w", err)
		}
		if errors.Is(err, unix.EEXIST) && sourceExists {
			return "", fmt.Errorf("finalize upload session: destination already exists")
		}
		if verifyErr := verifyUploadTree(target, files); verifyErr != nil {
			return "", fmt.Errorf("finalized upload conflict: %w", verifyErr)
		}
	}
	if err := unix.Fsync(targetRootFD); err != nil {
		return "", fmt.Errorf("sync incoming root: %w", err)
	}
	return target, nil
}

func (w UploadWriter) DeleteSession(sessionID string) error {
	if err := validateUploadRootAndID(w.Root, sessionID); err != nil {
		return err
	}
	path := filepath.Join(w.Root, sessionID)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("upload session path is not a directory")
	}
	return os.RemoveAll(path)
}

func validateUploadRootAndID(root, id string) error {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return fmt.Errorf("upload root must be absolute and canonical")
	}
	return domain.ValidateCandidateID(id)
}

func openUploadParent(sessionFD int, relative string) (int, string, error) {
	parts := strings.Split(relative, "/")
	current, err := unix.Dup(sessionFD)
	if err != nil {
		return -1, "", err
	}
	for _, part := range parts[:len(parts)-1] {
		next, openErr := unix.Openat(current, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if errors.Is(openErr, unix.ENOENT) {
			if mkdirErr := unix.Mkdirat(current, part, 0o750); mkdirErr != nil && !errors.Is(mkdirErr, unix.EEXIST) {
				unix.Close(current)
				return -1, "", fmt.Errorf("create upload directory: %w", mkdirErr)
			}
			next, openErr = unix.Openat(current, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		}
		unix.Close(current)
		if openErr != nil {
			return -1, "", fmt.Errorf("open upload directory: %w", openErr)
		}
		current = next
	}
	return current, parts[len(parts)-1], nil
}

func verifyUploadTree(root string, files []domain.UploadFileSpec) error {
	tree, err := Scan(root)
	if err != nil {
		return err
	}
	expected := make(map[string]int64, len(files))
	for _, file := range files {
		expected[file.RelativePath] = file.SizeBytes
	}
	if len(tree.Entries) != len(expected) {
		return fmt.Errorf("upload tree has %d files, expected %d", len(tree.Entries), len(expected))
	}
	for _, entry := range tree.Entries {
		size, exists := expected[entry.RelativePath]
		if !exists || size != entry.Size {
			return fmt.Errorf("unexpected upload entry %q", entry.RelativePath)
		}
	}
	return nil
}

func sameReaderAndFile(ctx context.Context, reader io.Reader, fileFD int, size int64) (bool, error) {
	incomingHash, incomingSize, err := hashExact(contextReader{ctx: ctx, reader: reader}, size)
	if err != nil {
		return false, err
	}
	existingHash, existingSize, err := hashFileDescriptor(fileFD, size)
	if err != nil {
		return false, err
	}
	return incomingSize == existingSize && bytes.Equal(incomingHash[:], existingHash[:]), nil
}

func sameFilesAt(directoryFD int, left, right string, size int64) (bool, error) {
	leftFD, err := unix.Openat(directoryFD, left, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return false, err
	}
	defer unix.Close(leftFD)
	rightFD, err := unix.Openat(directoryFD, right, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return false, err
	}
	defer unix.Close(rightFD)
	leftHash, leftSize, err := hashFileDescriptor(leftFD, size)
	if err != nil {
		return false, err
	}
	rightHash, rightSize, err := hashFileDescriptor(rightFD, size)
	return leftSize == rightSize && bytes.Equal(leftHash[:], rightHash[:]), err
}

func hashFileDescriptor(fileDescriptor int, expected int64) ([sha256.Size]byte, int64, error) {
	duplicate, err := unix.Dup(fileDescriptor)
	if err != nil {
		return [sha256.Size]byte{}, 0, err
	}
	file := os.NewFile(uintptr(duplicate), "upload-hash")
	defer file.Close()
	return hashExact(file, expected)
}

func hashExact(reader io.Reader, expected int64) ([sha256.Size]byte, int64, error) {
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(reader, expected+1))
	if err != nil {
		return [sha256.Size]byte{}, written, err
	}
	if written != expected {
		return [sha256.Size]byte{}, written, fmt.Errorf("upload retry has %d bytes, expected %d", written, expected)
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result, written, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.reader.Read(buffer)
	}
}
