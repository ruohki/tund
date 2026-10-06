//go:build !darwin && !linux && !freebsd && !windows

package fileserve

import "errors"

// volumeSpace is unknown here; uploads then skip the free-space floor.
func volumeSpace(string) (free, total uint64, err error) {
	return 0, 0, errors.ErrUnsupported
}
