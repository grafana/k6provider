package k6provider

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"syscall"
)

var (
	// errLocked is returned when the file is already locked
	errLocked = errors.New("file already locked")
	// errLockFailed is returned when there's an error accessing the lock file
	errLockFailed = errors.New("failed to lock file")
	// errUnLockFailed is returned when there's an error unlocking the file
	errUnLockFailed = errors.New("failed to lock file")
)

// A dirLock prevents concurrent access to a directory.
// Only works on unix-like systems. Windows is not supported.
// This code is inspired on the golang's filelock package:
// https://pkg.go.dev/cmd/go/internal/lockedfile/internal/filelock
type dirLock struct {
	mutex    sync.Mutex
	lockFile string
	fd       int
}

func newFileLock(path string) *dirLock {
	return &dirLock{
		lockFile: filepath.Join(path, "k6provider.lock"),
		fd:       -1,
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
	if m.fd != -1 {
		return nil
	}

	fd, err := syscall.Open(m.lockFile, syscall.O_RDWR|syscall.O_CREAT, 0o600)
	if err != nil {
		return fmt.Errorf("%w %w", errLockFailed, err)
	}
	err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		m.fd = fd
		return nil
	}

	if errors.Is(err, syscall.EWOULDBLOCK) {
		return errLocked
	}

	return fmt.Errorf("%w %w", errLockFailed, err)
}

func (m *dirLock) unlock() error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	// if file is not open, assume already unlocked
	if m.fd == -1 {
		return nil
	}

	defer func() {
		_ = syscall.Close(m.fd)
		m.fd = -1
	}()

	err := syscall.Flock(m.fd, syscall.LOCK_UN)
	if err != nil {
		return fmt.Errorf("%w %w", errUnLockFailed, err)
	}
	return nil
}
