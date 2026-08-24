// Package fscheck validates Denyra's filesystem ownership and atomic-move boundaries.
package fscheck

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Layout struct {
	DataRoot               string
	DownloadsSlskd         string
	DownloadsSpotiFLAC     string
	DownloadsOther         string
	IncomingManual         string
	IncomingUploading      string
	Work                   string
	Approved               string
	Quarantine             string
	Library                string
	LibraryUnmanaged       string
	ExpectedUID            int
	ExpectedGID            int
	MinimumMode            os.FileMode
	RequireLibraryReadOnly bool
}

type PathReport struct {
	Name      string
	Canonical string
	DeviceID  uint64
	UID       uint32
	GID       uint32
	Mode      os.FileMode
	Access    string
}

type Report struct{ Paths []PathReport }

type Directory struct {
	Name string
	Path string
}

func CheckDirectories(dataRoot string, directories []Directory, expectedUID, expectedGID int, minimumMode os.FileMode) (Report, error) {
	root, err := canonicalNoFollow(dataRoot)
	if err != nil {
		return Report{}, fmt.Errorf("data root: %w", err)
	}
	if len(directories) == 0 {
		return Report{}, fmt.Errorf("at least one mounted directory is required")
	}
	report := Report{Paths: make([]PathReport, 0, len(directories))}
	var mountedDevice uint64
	for _, directory := range directories {
		canonical, err := canonicalNoFollow(directory.Path)
		if err != nil {
			return Report{}, fmt.Errorf("%s: %w", directory.Name, err)
		}
		if canonical != root && !strings.HasPrefix(canonical, root+string(filepath.Separator)) {
			return Report{}, fmt.Errorf("%s path %q is outside data root", directory.Name, canonical)
		}
		device, uid, gid, mode, err := statIdentity(canonical)
		if err != nil {
			return Report{}, fmt.Errorf("%s: %w", directory.Name, err)
		}
		if mountedDevice == 0 {
			mountedDevice = device
		} else if device != mountedDevice {
			return Report{}, fmt.Errorf("%s is on device %d, mounted data paths are on %d", directory.Name, device, mountedDevice)
		}
		if int(uid) != expectedUID || int(gid) != expectedGID {
			return Report{}, fmt.Errorf("%s owner is %d:%d, expected %d:%d", directory.Name, uid, gid, expectedUID, expectedGID)
		}
		if mode.Perm()&minimumMode != minimumMode {
			return Report{}, fmt.Errorf("%s mode %04o lacks minimum %04o", directory.Name, mode.Perm(), minimumMode)
		}
		report.Paths = append(report.Paths, PathReport{Name: directory.Name, Canonical: canonical, DeviceID: device, UID: uid, GID: gid, Mode: mode.Perm(), Access: "read-write"})
	}
	return report, nil
}

func Check(layout Layout) (Report, error) {
	root, err := canonicalNoFollow(layout.DataRoot)
	if err != nil {
		return Report{}, fmt.Errorf("data root: %w", err)
	}
	paths := []struct {
		name, path string
		readOnly   bool
	}{
		{"downloads_slskd", layout.DownloadsSlskd, false},
		{"downloads_spotiflac", layout.DownloadsSpotiFLAC, false},
		{"downloads_other", layout.DownloadsOther, false},
		{"incoming_manual", layout.IncomingManual, false},
		{"incoming_uploading", layout.IncomingUploading, false},
		{"work", layout.Work, false},
		{"approved", layout.Approved, false},
		{"quarantine", layout.Quarantine, false},
		{"library", layout.Library, layout.RequireLibraryReadOnly},
		{"library_unmanaged", layout.LibraryUnmanaged, false},
	}
	report := Report{Paths: make([]PathReport, 0, len(paths))}
	var mountedDevice uint64
	for _, item := range paths {
		canonical, err := canonicalNoFollow(item.path)
		if err != nil {
			return Report{}, fmt.Errorf("%s: %w", item.name, err)
		}
		if canonical != root && !strings.HasPrefix(canonical, root+string(filepath.Separator)) {
			return Report{}, fmt.Errorf("%s path %q is outside data root", item.name, canonical)
		}
		device, uid, gid, mode, err := statIdentity(canonical)
		if err != nil {
			return Report{}, fmt.Errorf("%s: %w", item.name, err)
		}
		if mountedDevice == 0 {
			mountedDevice = device
		} else if device != mountedDevice {
			return Report{}, fmt.Errorf("%s is on device %d, mounted data paths are on %d", item.name, device, mountedDevice)
		}
		if int(uid) != layout.ExpectedUID || int(gid) != layout.ExpectedGID {
			return Report{}, fmt.Errorf("%s owner is %d:%d, expected %d:%d", item.name, uid, gid, layout.ExpectedUID, layout.ExpectedGID)
		}
		if mode.Perm()&layout.MinimumMode != layout.MinimumMode {
			return Report{}, fmt.Errorf("%s mode %04o lacks minimum %04o", item.name, mode.Perm(), layout.MinimumMode)
		}
		if item.readOnly && mode.Perm()&0o222 != 0 {
			return Report{}, fmt.Errorf("%s must be read-only for pipeline", item.name)
		}
		access := "read-write"
		if item.readOnly {
			access = "read-only"
		}
		report.Paths = append(report.Paths, PathReport{Name: item.name, Canonical: canonical, DeviceID: device, UID: uid, GID: gid, Mode: mode.Perm(), Access: access})
	}
	return report, nil
}

func canonicalNoFollow(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("path must be absolute")
	}
	clean := filepath.Clean(path)
	current := string(filepath.Separator)
	for _, segment := range strings.Split(strings.TrimPrefix(clean, string(filepath.Separator)), string(filepath.Separator)) {
		if segment == "" {
			continue
		}
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("symlink component rejected: %s", current)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("non-directory component rejected: %s", current)
		}
	}
	return clean, nil
}

func SameDevice(left, right string) (bool, error) {
	leftDevice, _, _, _, err := statIdentity(left)
	if err != nil {
		return false, err
	}
	rightDevice, _, _, _, err := statIdentity(right)
	if err != nil {
		return false, err
	}
	return leftDevice == rightDevice, nil
}
