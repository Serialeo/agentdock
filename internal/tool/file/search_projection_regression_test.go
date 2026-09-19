package file

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRipgrepContextStaysWithinEachMatchWindow(t *testing.T) {
	service, root := newCodeToolsRuntime(t)
	var data bytes.Buffer
	emit := func(kind string, n int, text string) {
		event := map[string]any{"type": kind, "data": map[string]any{"path": map[string]any{"text": filepath.Join(root, "code.go")}, "lines": map[string]any{"text": text + "\n"}, "line_number": n, "submatches": []any{map[string]any{"start": 0, "match": map[string]any{"text": "hit"}}}}}
		if err := json.NewEncoder(&data).Encode(event); err != nil {
			t.Fatal(err)
		}
	}
	emit("context", 9, "before first")
	emit("match", 10, "hit first")
	emit("context", 11, "after first")
	emit("context", 99, "before second")
	emit("match", 100, "hit second")
	emit("context", 101, "after second")
	matches, truncated, ok := service.parseRGJSON(data.Bytes(), root, SearchOptions{ContextLines: 1, MaxResults: 2})
	if !ok || truncated || len(matches) != 2 {
		t.Fatalf("result=%v truncated=%v ok=%v", matches, truncated, ok)
	}
	for i, item := range matches {
		before := item["before"].([]string)
		after := item["after"].([]string)
		if len(before) != 1 || len(after) != 1 {
			t.Fatalf("match %d context leaked across a gap: %#v", i, item)
		}
	}
	if matches[0]["context_end_line"] != 11 || matches[1]["context_start_line"] != 99 {
		t.Fatalf("incorrect line ranges: %#v", matches)
	}
	// Exactly max_results is complete; only observing another match justifies truncation.
	emit("match", 200, "hit third")
	matches, truncated, ok = service.parseRGJSON(data.Bytes(), root, SearchOptions{ContextLines: 1, MaxResults: 2})
	if !ok || !truncated || len(matches) != 2 {
		t.Fatal("lookahead failed to mark real truncation")
	}
}
func TestSearchSingleFileAndFallbackExactLimit(t *testing.T) {
	service, root := newCodeToolsRuntime(t)
	path := filepath.Join(root, "code.txt")
	if err := os.WriteFile(path, []byte("needle\ntail\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, err := service.ws.ResolveExisting(path)
	if err != nil {
		t.Fatal(err)
	}
	opts := SearchOptions{Query: "needle", MaxResults: 1, ContextLines: 1, CaseSensitive: true}
	result, err := service.searchTextGo(t.Context(), resolved, opts)
	if err != nil {
		t.Fatal(err)
	}
	if result["truncated"] != false {
		t.Fatalf("fallback falsely truncated exact limit: %#v", result)
	}
	if _, err := exec.LookPath("rg"); err == nil {
		result, available, err := service.searchTextRG(t.Context(), resolved, opts)
		if err != nil || !available {
			t.Fatalf("single-file rg search failed: %v", err)
		}
		if result["truncated"] != false {
			t.Fatal("single-file rg falsely truncated exact limit")
		}
	}
}
