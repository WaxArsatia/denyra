package filesystem

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

var ErrLockHeld = errors.New("filesystem release lock is held")

type Lock struct {
	file *os.File
}

func AcquireLock(path string) (*Lock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrLockHeld
		}
		return nil, fmt.Errorf("lock release: %w", err)
	}
	return &Lock{file: file}, nil
}

func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	_ = unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
	return l.file.Close()
}
