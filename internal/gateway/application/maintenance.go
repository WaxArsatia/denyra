package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/waxarsatia/denyra/internal/platform/storage"
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
	result, err := storage.Evaluate(controller.DataRoot, uint64(controller.MinimumFreeBytes), controller.MinimumFreePercent, func(path string) (storage.Capacity, error) {
		current, err := capacity(path)
		return storage.Capacity{AvailableBytes: current.FreeBytes, TotalBytes: current.TotalBytes}, err
	})
	if err != nil {
		return err
	}
	if !result.Allowed {
		return fmt.Errorf("%w: /data free=%d required=%d", ErrAdmissionBlocked, result.AvailableBytes, result.RequiredBytes)
	}
	return nil
}

func filesystemCapacity(path string) (Capacity, error) {
	result, err := storage.Evaluate(path, 0, 0, nil)
	return Capacity{FreeBytes: result.AvailableBytes, TotalBytes: result.TotalBytes}, err
}
