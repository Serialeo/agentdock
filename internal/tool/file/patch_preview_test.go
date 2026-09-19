package file

import "testing"

func TestStagedChangeStats(t *testing.T) {
	empty := ""
	same := "same\n"
	updated := "updated\n"
	staged := map[string]stagedPatchFile{
		"new-empty": {Content: &empty},
		"same":      {Content: &same, Original: []byte(same), OriginalExists: true},
		"updated":   {Content: &updated, Original: []byte(same), OriginalExists: true},
		"deleted":   {Original: []byte(same), OriginalExists: true},
		"no-op":     {},
	}
	want := diffStats{FilesChanged: 3, Insertions: 1, Deletions: 2}
	if got := stagedChangeStats(staged); got != want {
		t.Fatalf("stagedChangeStats() = %#v, want %#v", got, want)
	}
}
