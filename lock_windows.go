//go:build windows
// +build windows

package k6provider

import (
	"fmt"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"
)

const (
	lockfileFailImmediately = 1
	lockfileExclusiveLock   = 2
)

var (
	modkernel32      = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = modkernel32.NewProc("LockFileEx")
	procUnlockFileEx = modkernel32.NewProc("UnlockFileEx")
)

// A dirLock prevents concurrent access to a directory.
// This code is inspired on the golang's fslock package:
// https://github.com/juju/fslock/blob/master/fslock_windows.go
type dirLock struct {
	mutex    sync.Mutex
	lockFile string
	handle   syscall.Handle
}

func newFileLock(path string) *dirLock {
	return &dirLock{
		lockFile: filepath.Join(path, "k6provider.lock"),
	}
}

// lock places an advisory write lock on the directory's lock file.
// If the directory is blocked, returns ErrLocked.
// If lock returns nil, no other process will be able to place a lock until
// this process exits or unlocks it.
func (m *dirLock) lock() error {
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
		procLockFileEx.Addr(),
		uintptr(handle),
		uintptr(lockfileExclusiveLock|lockfileFailImmediately), // request exclusive lock and fail if not possible
		uintptr(0), // reserved
		uintptr(0), // range of bytes to lock (low)
		uintptr(1), // range of bytes to lock (high)
		uintptr(unsafe.Pointer(&syscall.Overlapped{HEvent: syscall.InvalidHandle})), // pass an overlap without event handle
	)
	if r1 == 0 { // the call failed
		if e1 != 0 { // e1 is the error code, if it's not 0, there was an error
			err = error(e1)
		} else { // otherwise, the error is unknown
			err = syscall.EINVAL
		}
	}

	if err == nil {
		m.handle = handle
		return nil
	}

	return fmt.Errorf("%w %w", errLockFailed, err)
}

func (m *dirLock) unlock() error {
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
		procUnlockFileEx.Addr(),
		uintptr(m.handle),
		uintptr(0), // reserved
		uintptr(0), // range of bytes to lock (low)
		uintptr(1), // range of bytes to lock (high)
		uintptr(unsafe.Pointer(&syscall.Overlapped{HEvent: syscall.InvalidHandle})), // pass an overlap without event handle
	)
	var err error
	if r1 == 0 { // the call failed
		if e1 != 0 { // e1 is the error code, if it's not 0, there was an error
			err = error(e1)
		} else { // otherwise, the error is unknown
			err = syscall.EINVAL
		}
	}

	if err != nil {
		return fmt.Errorf("%w %w", errUnLockFailed, err)
	}
	return nil
}
