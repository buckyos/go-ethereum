//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd
// +build darwin dragonfly freebsd linux netbsd openbsd

package rawdb

import (
	"os"
	"syscall"

	"github.com/prometheus/tsdb/fileutil"
)

type readOnlyFreezerLock struct{ file *os.File }

// Closing the descriptor also releases its exclusive advisory lock.
func (l *readOnlyFreezerLock) Release() error { return l.file.Close() }

func lockFreezerReadOnly(path string) (fileutil.Releaser, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, err
	}
	return &readOnlyFreezerLock{file: file}, nil
}
