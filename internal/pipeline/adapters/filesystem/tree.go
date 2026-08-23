package filesystem

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

type Entry struct {
	RelativePath string
	Size         int64
	MTimeNS      int64
	Device       uint64
	Inode        uint64
	Mode         uint32
}

type Tree struct {
	Root        string
	Device      uint64
	Entries     []Entry
	Fingerprint string
}

var ErrUnsafeTree = errors.New("unsafe release tree")

func Scan(root string) (Tree, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return Tree{}, fmt.Errorf("%w: root must be an absolute canonical path", ErrUnsafeTree)
	}
	rootFD, err := openAbsoluteDirectoryNoFollow(root)
	if err != nil {
		return Tree{}, fmt.Errorf("%w: open root: %v", ErrUnsafeTree, err)
	}
	defer unix.Close(rootFD)
	var rootStat unix.Stat_t
	if err := unix.Fstat(rootFD, &rootStat); err != nil {
		return Tree{}, err
	}
	entries := make([]Entry, 0)
	if err := walkDirectory(rootFD, "", uint64(rootStat.Dev), &entries); err != nil {
		return Tree{}, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].RelativePath < entries[j].RelativePath })
	hash := sha256.New()
	for _, entry := range entries {
		for _, field := range []string{
			entry.RelativePath, strconv.FormatInt(entry.Size, 10), strconv.FormatInt(entry.MTimeNS, 10),
			strconv.FormatUint(entry.Device, 10), strconv.FormatUint(entry.Inode, 10), strconv.FormatUint(uint64(entry.Mode), 10),
		} {
			_, _ = hash.Write([]byte(field))
			_, _ = hash.Write([]byte{0})
		}
	}
	return Tree{Root: root, Device: uint64(rootStat.Dev), Entries: entries, Fingerprint: hex.EncodeToString(hash.Sum(nil))}, nil
}

func openAbsoluteDirectoryNoFollow(path string) (int, error) {
	current, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	for _, component := range strings.Split(strings.TrimPrefix(path, "/"), string(filepath.Separator)) {
		if component == "" {
			continue
		}
		next, openErr := unix.Openat(current, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		unix.Close(current)
		if openErr != nil {
			return -1, openErr
		}
		current = next
	}
	return current, nil
}

func walkDirectory(directoryFD int, prefix string, rootDevice uint64, entries *[]Entry) error {
	duplicate, err := unix.Dup(directoryFD)
	if err != nil {
		return err
	}
	directory := os.NewFile(uintptr(duplicate), "release-tree")
	names, err := directory.ReadDir(-1)
	_ = directory.Close()
	if err != nil {
		return err
	}
	for _, item := range names {
		name := item.Name()
		if name == "." || name == ".." || strings.ContainsRune(name, filepath.Separator) {
			return fmt.Errorf("%w: invalid directory entry %q", ErrUnsafeTree, name)
		}
		var before unix.Stat_t
		if err := unix.Fstatat(directoryFD, name, &before, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return err
		}
		if uint64(before.Dev) != rootDevice {
			return fmt.Errorf("%w: nested filesystem at %s", ErrUnsafeTree, filepath.Join(prefix, name))
		}
		mode := before.Mode & unix.S_IFMT
		relative := filepath.ToSlash(filepath.Join(prefix, name))
		switch mode {
		case unix.S_IFDIR:
			child, err := unix.Openat(directoryFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
			if err != nil {
				return fmt.Errorf("%w: open directory %s: %v", ErrUnsafeTree, relative, err)
			}
			var after unix.Stat_t
			err = unix.Fstat(child, &after)
			if err == nil && (after.Dev != before.Dev || after.Ino != before.Ino) {
				err = fmt.Errorf("%w: directory identity changed at %s", ErrUnsafeTree, relative)
			}
			if err == nil {
				err = walkDirectory(child, relative, rootDevice, entries)
			}
			unix.Close(child)
			if err != nil {
				return err
			}
		case unix.S_IFREG:
			file, err := unix.Openat(directoryFD, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
			if err != nil {
				return fmt.Errorf("%w: open regular file %s: %v", ErrUnsafeTree, relative, err)
			}
			var after unix.Stat_t
			err = unix.Fstat(file, &after)
			unix.Close(file)
			if err != nil {
				return err
			}
			if after.Dev != before.Dev || after.Ino != before.Ino || after.Mode&unix.S_IFMT != unix.S_IFREG {
				return fmt.Errorf("%w: file identity changed at %s", ErrUnsafeTree, relative)
			}
			*entries = append(*entries, Entry{RelativePath: relative, Size: after.Size, MTimeNS: after.Mtim.Sec*1_000_000_000 + after.Mtim.Nsec, Device: uint64(after.Dev), Inode: after.Ino, Mode: after.Mode})
		default:
			return fmt.Errorf("%w: non-regular entry at %s", ErrUnsafeTree, relative)
		}
	}
	return nil
}
