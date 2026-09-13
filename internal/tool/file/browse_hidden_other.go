//go:build !windows && !darwin

package file

import (
	"os"
	"strings"
)

func browseHiddenEntry(entry os.DirEntry) (bool, os.FileInfo, error) {
	return strings.HasPrefix(entry.Name(), "."), nil, nil
}
