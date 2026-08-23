// Package storage centralizes admission against the filesystem containing /data.
package storage

import (
	"fmt"
	"os"
	"syscall"
)

type Admission struct {
	Allowed          bool
	AvailableBytes   uint64
	TotalBytes       uint64
	RequiredBytes    uint64
	RequiredPercent  float64
	FilesystemDevice uint64
}

type Capacity struct{ AvailableBytes, TotalBytes, Device uint64 }

func Evaluate(path string, minimumBytes uint64, minimumPercent float64, capacity func(string) (Capacity, error)) (Admission, error) {
	if path == "" || minimumPercent < 0 || minimumPercent > 100 {
		return Admission{}, fmt.Errorf("invalid storage admission policy")
	}
	if capacity == nil {
		capacity = filesystemCapacity
	}
	value, err := capacity(path)
	if err != nil {
		return Admission{}, err
	}
	percentBytes := uint64(float64(value.TotalBytes) * minimumPercent / 100)
	required := minimumBytes
	if percentBytes > required {
		required = percentBytes
	}
	return Admission{Allowed: value.AvailableBytes >= required, AvailableBytes: value.AvailableBytes, TotalBytes: value.TotalBytes, RequiredBytes: required, RequiredPercent: minimumPercent, FilesystemDevice: value.Device}, nil
}

func filesystemCapacity(path string) (Capacity, error) {
	var statfs syscall.Statfs_t
	if err := syscall.Statfs(path, &statfs); err != nil {
		return Capacity{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return Capacity{}, err
	}
	var device uint64
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		device = stat.Dev
	}
	return Capacity{AvailableBytes: statfs.Bavail * uint64(statfs.Bsize), TotalBytes: statfs.Blocks * uint64(statfs.Bsize), Device: device}, nil
}
