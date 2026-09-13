//go:build windows

package file

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestBrowseHiddenEntryRecognizesWindowsHiddenAttribute(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "hidden-by-attribute.txt")
	if err := os.WriteFile(path, []byte("hidden"), 0o600); err != nil {
		t.Fatal(err)
	}
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetFileAttributes(pointer, windows.FILE_ATTRIBUTE_HIDDEN); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	hidden, info, err := browseHiddenEntry(entries[0])
	if err != nil {
		t.Fatal(err)
	}
	if !hidden || info == nil {
		t.Fatalf("hidden=%v info=%#v", hidden, info)
	}
}
