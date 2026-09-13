//go:build windows

package command

import (
	"os"

	"github.com/uvwt/agentdock/internal/fs/securepath"
	"golang.org/x/sys/windows"
)

func lockCommandJournal(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := securepath.EnsurePrivate(path); err != nil {
		_ = file.Close()
		return nil, err
	}
	overlapped := new(windows.Overlapped)
	if err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, overlapped); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}
