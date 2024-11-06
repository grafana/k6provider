package k6provider

import (
	"errors"
	"testing"
)

func TestLock(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// this is the original lock
	l := newFileLock(dir)

	// should lock dir without errors
	if err := l.lock(); err != nil {
		t.Fatalf("unexpected %v", err)
	}

	//  locking again should return without errors
	if err := l.lock(); err != nil {
		t.Fatalf("unexpected %v", err)
	}

	// another lock should return ErrLocked
	if err := newFileLock(dir).lock(); !errors.Is(err, ErrLocked) {
		t.Fatalf("unexpected %v", err)
	}

	// locking another directory return without errors
	if err := newFileLock(t.TempDir()).lock(); err != nil {
		t.Fatalf("unexpected %v", err)
	}

	// unlock should work
	if err := l.unlock(); err != nil {
		t.Fatalf("unexpected %v", err)
	}

	// unlocking again should return without errors
	if err := l.unlock(); err != nil {
		t.Fatalf("unexpected %v", err)
	}

	// trying another lock again should work now
	if err := newFileLock(dir).lock(); err != nil {
		t.Fatalf("unexpected %v", err)
	}

	// retrying original lock should return ErrLocked
	if err := l.lock(); !errors.Is(err, ErrLocked) {
		t.Fatalf("unexpected %v", err)
	}

	// trying to lock a non-existing dir should fails
	if err := newFileLock("/path/to/non/existing/dir").lock(); !errors.Is(err, ErrLockFailed) {
		t.Fatalf("unexpected %v", err)
	}
}
