//go:build windows

package filelock

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestOwnerInspectionAllowsConcurrentRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owner")
	if err := os.WriteFile(path, []byte("1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	reader, err := openOwnerFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	// 故意保持读取句柄打开，确保释放者仍能删除锁文件。
	if err := os.Remove(path); err != nil {
		t.Fatalf("owner inspection prevented release: %v", err)
	}
}

func TestRetryableLockCreationErrorOnWindows(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "exists", err: os.ErrExist, want: true},
		{name: "access denied while deleting", err: windows.ERROR_ACCESS_DENIED, want: true},
		{name: "sharing violation", err: windows.ERROR_SHARING_VIOLATION, want: true},
		{name: "invalid name", err: windows.ERROR_INVALID_NAME, want: false},
		{name: "unrelated", err: errors.New("unrelated"), want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := retryableLockCreationError(test.err); got != test.want {
				t.Fatalf("retryableLockCreationError(%v) = %v, want %v", test.err, got, test.want)
			}
		})
	}
}
