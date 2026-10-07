//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || windows
// +build darwin dragonfly freebsd linux netbsd openbsd windows

package rawdb

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/prometheus/tsdb/fileutil"
)

func TestReadOnlyFreezerLockNeverCreatesAndExcludesWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "FLOCK")
	if lock, err := lockFreezerReadOnly(path); err == nil {
		lock.Release()
		t.Fatal("missing lock file was accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("read-only lock created a file", err)
	}
	writer, _, err := fileutil.Flock(path)
	if err != nil {
		t.Fatal(err)
	}
	if lock, err := lockFreezerReadOnly(path); err == nil {
		lock.Release()
		t.Fatal("reader accepted a live writer")
	}
	if err := writer.Release(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0400); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(path, 0600)
	reader, err := lockFreezerReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := lockFreezerReadOnly(path); err == nil {
		other.Release()
		t.Fatal("two exclusive readers acquired the lock")
	}
	if err := reader.Release(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	reader, err = lockFreezerReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	if other, _, err := fileutil.Flock(path); err == nil {
		other.Release()
		t.Fatal("writer acquired an inspection lock")
	}
	reader.Release()
	writer, _, err = fileutil.Flock(path)
	if err != nil {
		t.Fatal("inspection did not release lock", err)
	}
	writer.Release()
}
