//go:build windows

package fs

import (
	"fmt"
	"syscall"
	"unsafe"
)

// Windows-specific constants and DLL procedures for file locking
var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

// Windows LOCKFILE flag values (see LockFileEx documentation:
// https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-lockfileex).
const (
	lockfileFailImmediately = uint32(0x00000001) // LOCKFILE_FAIL_IMMEDIATELY
	lockfileExclusiveLock   = uint32(0x00000002) // LOCKFILE_EXCLUSIVE_LOCK
)

// overlapped structure required by the Windows LockFileEx API
type overlapped struct {
	Internal     uintptr
	InternalHigh uintptr
	Offset       uint32
	OffsetHigh   uint32
	HEvent       syscall.Handle
}

// platformLock acquires a shared or exclusive lock using the Windows LockFileEx API.
// When tryOnly is true the call is non-blocking and returns an error immediately if
// the lock cannot be acquired.
func (fl *FileLock) platformLock(lockType LockType, tryOnly bool) error {
	var flags uint32

	if tryOnly {
		flags |= lockfileFailImmediately
	}
	if lockType == LockExclusive {
		flags |= lockfileExclusiveLock
	}

	ol := overlapped{}
	ret, _, err := procLockFileEx.Call(
		uintptr(fl.file.Fd()),
		uintptr(flags),
		uintptr(0),          // reserved
		uintptr(0xFFFFFFFF), // low bytes to lock
		uintptr(0xFFFFFFFF), // high bytes to lock
		uintptr(unsafe.Pointer(&ol)),
	)
	if ret == 0 {
		return fmt.Errorf("LockFileEx failed: %w", err)
	}
	return nil
}

// platformUnlock releases a lock using the Windows UnlockFileEx API.
func (fl *FileLock) platformUnlock() error {
	ol := overlapped{}
	ret, _, err := procUnlockFileEx.Call(
		uintptr(fl.file.Fd()),
		uintptr(0),          // reserved
		uintptr(0xFFFFFFFF), // low bytes to unlock
		uintptr(0xFFFFFFFF), // high bytes to unlock
		uintptr(unsafe.Pointer(&ol)),
	)
	if ret == 0 {
		return fmt.Errorf("UnlockFileEx failed: %w", err)
	}
	return nil
}
