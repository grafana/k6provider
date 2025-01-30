package k6provider

import "errors"

var (
	// errLocked is returned when the file is already locked
	errLocked = errors.New("file already locked")
	// errLockFailed is returned when there's an error accessing the lock file
	errLockFailed = errors.New("failed to lock file")
	// errUnLockFailed is returned when there's an error unlocking the file
	errUnLockFailed = errors.New("failed to lock file")
)
