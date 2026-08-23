package application

import (
	"context"
	"errors"
	"fmt"
	"syscall"
)

var ErrAdmissionBlocked = errors.New("new acquisition admission is blocked")

type Capacity struct {
	FreeBytes, TotalBytes uint64
}

type AdmissionController struct {
	Store interface {
		Maintenance(context.Context) (bool, string, error)
	}
	DataRoot           string
	MinimumFreeBytes   int64
	MinimumFreePercent float64
	Capacity           func(string) (Capacity, error)
}

func (controller AdmissionController) CheckNew(ctx context.Context) error {
	if controller.Store == nil || controller.DataRoot == "" || controller.MinimumFreeBytes < 0 || controller.MinimumFreePercent < 0 || controller.MinimumFreePercent > 100 {
		return fmt.Errorf("admission controller is not configured")
	}
	maintenance, reason, err := controller.Store.Maintenance(ctx)
	if err != nil {
		return err
	}
	if maintenance {
		return fmt.Errorf("%w: maintenance: %s", ErrAdmissionBlocked, reason)
	}
	capacity := controller.Capacity
	if capacity == nil {
		capacity = filesystemCapacity
	}
	current, err := capacity(controller.DataRoot)
	if err != nil {
		return err
	}
	percentThreshold := uint64(float64(current.TotalBytes) * controller.MinimumFreePercent / 100)
	threshold := uint64(controller.MinimumFreeBytes)
	if percentThreshold > threshold {
		threshold = percentThreshold
	}
	if current.FreeBytes < threshold {
		return fmt.Errorf("%w: /data free=%d required=%d", ErrAdmissionBlocked, current.FreeBytes, threshold)
	}
	return nil
}

func filesystemCapacity(path string) (Capacity, error) {
	var value syscall.Statfs_t
	if err := syscall.Statfs(path, &value); err != nil {
		return Capacity{}, err
	}
	return Capacity{FreeBytes: value.Bavail * uint64(value.Bsize), TotalBytes: value.Blocks * uint64(value.Bsize)}, nil
}
