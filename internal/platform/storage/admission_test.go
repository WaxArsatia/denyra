package storage_test

import (
	"github.com/waxarsatia/denyra/internal/platform/storage"
	"testing"
)

func TestAdmissionUsesMaximumAndAcceptsExactBoundary(t *testing.T) {
	capacity := func(string) (storage.Capacity, error) {
		return storage.Capacity{AvailableBytes: 50, TotalBytes: 1000, Device: 7}, nil
	}
	result, err := storage.Evaluate("/data", 20, 5, capacity)
	if err != nil || !result.Allowed || result.RequiredBytes != 50 || result.FilesystemDevice != 7 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	capacity = func(string) (storage.Capacity, error) {
		return storage.Capacity{AvailableBytes: 49, TotalBytes: 1000}, nil
	}
	result, err = storage.Evaluate("/data", 20, 5, capacity)
	if err != nil || result.Allowed {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
