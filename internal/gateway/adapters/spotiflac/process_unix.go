//go:build unix

package spotiflac

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

func configureProcessGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func signalProcessGroup(command *exec.Cmd, signal syscall.Signal) error {
	if command.Process == nil {
		return nil
	}
	err := syscall.Kill(-command.Process.Pid, signal)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func terminateProcessGroup(command *exec.Cmd, waited <-chan error, grace time.Duration) error {
	_ = signalProcessGroup(command, syscall.SIGTERM)
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case err := <-waited:
		return err
	case <-timer.C:
		_ = signalProcessGroup(command, syscall.SIGKILL)
		return <-waited
	}
}
