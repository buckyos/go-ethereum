package rawdb

import (
	"syscall"

	"github.com/prometheus/tsdb/fileutil"
)

type readOnlyFreezerLock struct{ handle syscall.Handle }

func (l *readOnlyFreezerLock) Release() error { return syscall.Close(l.handle) }

func lockFreezerReadOnly(path string) (fileutil.Releaser, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	// No sharing, creation or write access: fail if a writer already owns FLOCK.
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	return &readOnlyFreezerLock{handle: handle}, nil
}
