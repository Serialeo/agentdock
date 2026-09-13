package file

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/uvwt/agentdock/internal/workspace"
)

func TestBrowseDirIsShallowBoundedAndPaginates(t *testing.T) {
	root := t.TempDir()
	for index := 0; index < 620; index++ {
		name := filepath.Join(root, fmt.Sprintf("entry-%04d.txt", index))
		if err := os.WriteFile(name, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 20; index++ {
		if err := os.WriteFile(filepath.Join(root, "nested", fmt.Sprintf("child-%02d.txt", index)), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	ws, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	svc := New(ws, nil, nil)
	limit := MaxBrowseLimit
	first, err := svc.BrowseDir(context.Background(), BrowseRequest{Limit: &limit})
	if err != nil {
		t.Fatal(err)
	}
	entries := first["entries"].([]map[string]any)
	if len(entries) != MaxBrowseLimit || first["truncated"] != true || first["next_offset"] != MaxBrowseLimit {
		t.Fatalf("first page = %#v", first)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if filepath.Dir(entry["path"].(string)) != realRoot {
			t.Fatalf("browse descended below root: %#v", entry)
		}
	}

	offset := MaxBrowseLimit
	second, err := svc.BrowseDir(context.Background(), BrowseRequest{Offset: &offset, Limit: &limit})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(second["entries"].([]map[string]any)); got != 121 {
		t.Fatalf("second page entries = %d, want 121", got)
	}
	if second["truncated"] != false {
		t.Fatalf("second page unexpectedly truncated: %#v", second)
	}
}

func TestBrowseDirHiddenAndCanonicalNavigation(t *testing.T) {
	root := t.TempDir()
	visible := filepath.Join(root, "visible")
	if err := os.Mkdir(visible, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".secret"), []byte("hidden"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	svc := New(ws, nil, nil)

	result, err := svc.BrowseDir(context.Background(), BrowseRequest{})
	if err != nil {
		t.Fatal(err)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if result["path"] != realRoot || result["parent_path"] != filepath.Dir(realRoot) {
		t.Fatalf("navigation paths = %#v", result)
	}
	entries := result["entries"].([]map[string]any)
	if len(entries) != 1 || entries[0]["name"] != "visible" || entries[0]["path"] != filepath.Join(realRoot, "visible") || entries[0]["type"] != "directory" {
		t.Fatalf("default entries = %#v", entries)
	}

	withHidden, err := svc.BrowseDir(context.Background(), BrowseRequest{IncludeHidden: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(withHidden["entries"].([]map[string]any)); got != 2 {
		t.Fatalf("hidden entries count = %d, want 2", got)
	}
}

func TestBrowseBoundsRejectsUnboundedRequests(t *testing.T) {
	for _, test := range []struct {
		name   string
		offset int
		limit  int
	}{
		{name: "negative offset", offset: -1, limit: DefaultBrowseLimit},
		{name: "huge offset", offset: MaxBrowseOffset + 1, limit: DefaultBrowseLimit},
		{name: "zero limit", offset: 0, limit: 0},
		{name: "huge limit", offset: 0, limit: MaxBrowseLimit + 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := browseBounds(&test.offset, &test.limit); err == nil {
				t.Fatal("browseBounds unexpectedly accepted invalid request")
			}
		})
	}
}
