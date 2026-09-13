//go:build darwin

package file

import (
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func browseHiddenEntry(entry os.DirEntry) (bool, os.FileInfo, error) {
	info, err := entry.Info()
	if err != nil {
		return false, nil, err
	}
	hidden := strings.HasPrefix(entry.Name(), ".")
	if data, ok := info.Sys().(*syscall.Stat_t); ok {
		hidden = hidden || data.Flags&unix.UF_HIDDEN != 0
	}
	return hidden, info, nil
}
