//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !windows
// +build !darwin,!dragonfly,!freebsd,!linux,!netbsd,!openbsd,!windows

package rawdb

import (
	"fmt"
	"runtime"

	"github.com/prometheus/tsdb/fileutil"
)

func lockFreezerReadOnly(path string) (fileutil.Releaser, error) {
	return nil, fmt.Errorf("read-only freezer locking unsupported on %s: %s", runtime.GOOS, path)
}
