package filesystem

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

var ErrCrossDevice = errors.New("atomic release move crosses filesystem boundary")

func MoveAtomic(source, target string) error {
	if err := os.Rename(source, target); err != nil {
		if errors.Is(err, unix.EXDEV) {
			return ErrCrossDevice
		}
		return fmt.Errorf("atomic move %s to %s: %w", source, target, err)
	}
	return nil
}
