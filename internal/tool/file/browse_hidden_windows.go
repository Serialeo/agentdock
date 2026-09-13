//go:build windows

package file

import (
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

func browseHiddenEntry(entry os.DirEntry) (bool, os.FileInfo, error) {
	info, err := entry.Info()
	if err != nil {
		return false, nil, err
	}
	hidden := strings.HasPrefix(entry.Name(), ".")
	if data, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		hidden = hidden || data.FileAttributes&windows.FILE_ATTRIBUTE_HIDDEN != 0
	}
	return hidden, info, nil
}
