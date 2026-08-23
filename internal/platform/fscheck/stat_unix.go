//go:build unix

package fscheck

import (
	"fmt"
	"os"
	"syscall"
)

func statIdentity(path string) (device uint64, uid uint32, gid uint32, mode os.FileMode, err error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, 0, 0, fmt.Errorf("unsupported stat metadata for %s", path)
	}
	return uint64(stat.Dev), stat.Uid, stat.Gid, info.Mode(), nil
}
