package filesystem

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

var ErrTargetExists = errors.New("atomic move target already exists")

func MoveNoReplace(source, target string) error {
	err := unix.Renameat2(unix.AT_FDCWD, source, unix.AT_FDCWD, target, unix.RENAME_NOREPLACE)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.EXDEV):
		return ErrCrossDevice
	case errors.Is(err, unix.EEXIST), errors.Is(err, unix.ENOTEMPTY):
		return ErrTargetExists
	default:
		return fmt.Errorf("atomic no-replace move %s to %s: %w", source, target, err)
	}
}
