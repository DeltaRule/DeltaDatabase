//go:build !windows

package fs

import (
	"fmt"
	"syscall"
)

// platformLock acquires a shared or exclusive POSIX advisory lock via flock(2).
// When tryOnly is true the call is non-blocking and returns an error immediately
// if the lock cannot be acquired.
func (fl *FileLock) platformLock(lockType LockType, tryOnly bool) error {
	how := syscall.LOCK_SH
	if lockType == LockExclusive {
		how = syscall.LOCK_EX
	}
	if tryOnly {
		how |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(fl.file.Fd()), how); err != nil {
		return fmt.Errorf("flock failed: %w", err)
	}
	return nil
}

// platformUnlock releases the POSIX advisory lock via flock(2).
func (fl *FileLock) platformUnlock() error {
	if err := syscall.Flock(int(fl.file.Fd()), syscall.LOCK_UN); err != nil {
		return fmt.Errorf("flock unlock failed: %w", err)
	}
	return nil
}
