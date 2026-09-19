package file

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileEditAddCreatesEmptyFile(t *testing.T) {
	runtime, root := newFileTestService(t)
	result, err := runtime.editTest(context.Background(), map[string]any{
		"action":  "add",
		"path":    "empty.txt",
		"content": "",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["changed"] != true || result["files_changed"] != 1 ||
		result["insertions"] != 0 || result["deletions"] != 0 {
		t.Fatalf("expected empty file creation to report changed: %#v", result)
	}
	info, err := os.Stat(filepath.Join(root, "empty.txt"))
	if err != nil {
		t.Fatalf("expected empty file to be created: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("empty file size = %d", info.Size())
	}
}

func TestFileEditAddLargeContentDoesNotEchoDiffAfterWrite(t *testing.T) {
	runtime, root := newFileTestService(t)
	content := strings.Repeat("line with quotes \" and braces {x}\n", 1024)
	result, err := runtime.editTest(context.Background(), map[string]any{
		"action":  "add",
		"path":    "large.txt",
		"content": content,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["changed"] != true || result["files_changed"] != 1 ||
		result["insertions"] != 1024 || result["deletions"] != 0 {
		t.Fatalf("unexpected large add result: %#v", result)
	}
	if _, exists := result["diff_preview"]; exists {
		t.Fatalf("applied large add returned an unsolicited diff preview")
	}
	data, err := os.ReadFile(filepath.Join(root, "large.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Fatal("large add content changed during write")
	}
}
