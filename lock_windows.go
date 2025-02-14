//go:build windows
// +build windows

package k6provider

import (
	"fmt"
	"path/filepath"
	"sync"
	"syscall"
)

const (
	lockfileFailImmediately               = 1
	lockfileExclusiveLock                 = 2
	errnoLocked             syscall.Errno = 33
)

var (
	modkernel32    = syscall.NewLazyDLL("kernel32.dll")
	procLockFile   = modkernel32.NewProc("LockFile")
	procUnlockFile = modkernel32.NewProc("UnlockFile")
)

// A fileLock prevents concurrent access to a file.
// This code is inspired on the golang's fslock package:
// https://github.com/juju/fslock/blob/master/fslock_windows.go
type fileLock struct {
	mutex    sync.Mutex
	lockFile string
	handle   syscall.Handle
}

func newFileLock(path string) *fileLock {
	return &fileLock{
		lockFile: filepath.Join(path, "k6provider.lock"),
		handle:   syscall.InvalidHandle,
	}
}

// lock places an advisory write lock on the file.
// If the file is blocked, returns ErrLocked.
// If lock returns nil, no other process will be able to place a lock until
// this process exits or unlocks it.
func (m *fileLock) lock() error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	// file open, assume already locked
	if m.handle != syscall.InvalidHandle {
		return nil
	}

	lockfile, err := syscall.UTF16PtrFromString(m.lockFile)
	if err != nil {
		// TODO return a typed error
		return err
	}

	handle, err := syscall.CreateFile(
		lockfile,
		syscall.GENERIC_READ,
		syscall.FILE_SHARE_READ,
		nil,
		syscall.OPEN_ALWAYS,
		syscall.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return fmt.Errorf("%w %w", errLockFailed, err)
	}

	r1, _, e1 := syscall.SyscallN(
		procLockFile.Addr(),
		uintptr(handle),
		uintptr(0), // lock area offset (low)
		uintptr(0), // lock area offset (high)
		uintptr(1), // bytes to lock (low)
		uintptr(0), // bytes to lock (high)
	)
	if r1 == 0 { // the call failed
		_ = syscall.Close(handle)

		if syscall.Errno(e1) == errnoLocked {
			return errLocked
		}

		if e1 == 0 { // error code is unknown
			err = syscall.EINVAL
		}

		return fmt.Errorf("%w (errno %d) %s", errLockFailed, e1, error(e1).Error())
	}

	m.handle = handle
	return nil
}

func (m *fileLock) unlock() error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	// if file is not open, assume already unlocked
	if m.handle == syscall.InvalidHandle {
		return nil
	}

	defer func() {
		_ = syscall.Close(m.handle)
		m.handle = syscall.InvalidHandle
	}()

	r1, _, e1 := syscall.SyscallN(
		procUnlockFile.Addr(),
		uintptr(m.handle),
		uintptr(0), // lock area offset (low)
		uintptr(0), // lock area offset (high)
		uintptr(1), // bytes to lock (low)
		uintptr(0), // bytes to lock (high)
	)
	if r1 == 0 { // the call failed
		if e1 == 0 { // e1 is the error code, if it's not 0, there was an error
			e1 = syscall.EINVAL
		}
		return fmt.Errorf("%w %s", errUnLockFailed, error(e1).Error())
	}

	return nil
}
