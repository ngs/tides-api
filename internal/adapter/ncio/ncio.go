// Package ncio serializes access to the NetCDF C library (libnetcdf),
// which is not thread-safe. All NetCDF open/read/close sequences across
// the fes, bathymetry, and geoid adapters must hold this lock.
package ncio

import "sync"

//nolint:gochecknoglobals // Intentional: process-wide lock guarding non-thread-safe libnetcdf.
var mu sync.Mutex

// Lock acquires the global NetCDF I/O lock.
func Lock() { mu.Lock() }

// Unlock releases the global NetCDF I/O lock.
func Unlock() { mu.Unlock() }
