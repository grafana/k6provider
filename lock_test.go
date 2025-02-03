package k6provider

import (
	"errors"
	"testing"
)

func TestLock(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// this is the original lock
	firstLock := newFileLock(dir)

	// should lock dir without errors
	if err := firstLock.lock(); err != nil {
		t.Fatalf("unexpected %v", err)
	}

	//  locking again should return without errors
	if err := firstLock.lock(); err != nil {
		t.Fatalf("unexpected %v", err)
	}

	// another lock should return ErrLocked
	if err := newFileLock(dir).lock(); !errors.Is(err, errLocked) {
		t.Fatalf("unexpected %v", err)
	}

	// locking another directory return without errors
	anotherLock := newFileLock(t.TempDir())
	if err := anotherLock.lock(); err != nil {
		t.Fatalf("unexpected %v", err)
	}
	// must unlock or test can't clean up the tmp dir
	defer anotherLock.unlock() //nolint:errcheck

	// unlock should work
	if err := firstLock.unlock(); err != nil {
		t.Fatalf("unexpected %v", err)
	}

	// unlocking again should return without errors
	if err := firstLock.unlock(); err != nil {
		t.Fatalf("unexpected %v", err)
	}

	// trying another lock again should work now
	secondLock := newFileLock(dir)
	if err := secondLock.lock(); err != nil {
		t.Fatalf("unexpected %v", err)
	}
	// must unlock or test can't clean up the tmp dir
	defer secondLock.unlock() //nolint:errcheck

	// retrying original lock should return ErrLocked
	if err := firstLock.lock(); !errors.Is(err, errLocked) {
		t.Fatalf("unexpected %v", err)
	}

	// trying to lock a non-existing dir should fails
	if err := newFileLock("/path/to/non/existing/dir").lock(); !errors.Is(err, errLockFailed) {
		t.Fatalf("unexpected %v", err)
	}
}
