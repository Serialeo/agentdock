package file

import (
	"math/rand"
	"strings"
	"testing"
)

func TestUnifiedDiffPreviewWithoutExternalDiff(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	preview, truncated, stats, err := unifiedDiffPreview(
		"example.txt",
		"alpha\nkeep\n",
		"beta\nkeep\n",
		65536,
	)
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Fatal("small diff was truncated")
	}
	for _, want := range []string{
		"--- a/example.txt\n",
		"+++ b/example.txt\n",
		"-alpha\n",
		"+beta\n",
	} {
		if !strings.Contains(preview, want) {
			t.Fatalf("preview does not contain %q:\n%s", want, preview)
		}
	}
	if strings.HasPrefix(preview, "diff ") {
		t.Fatalf("preview contains an unexpected command header:\n%s", preview)
	}
	if stats != (diffStats{FilesChanged: 1, Insertions: 1, Deletions: 1}) {
		t.Fatalf("stats = %#v", stats)
	}
}

func TestUnifiedDiffPreviewPreservesMissingNewlineMarkers(t *testing.T) {
	preview, _, _, err := unifiedDiffPreview("example.txt", "alpha", "beta", 65536)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(preview, "\\ No newline at end of file"); got != 2 {
		t.Fatalf("missing newline markers = %d, want 2:\n%s", got, preview)
	}
}

func TestUnifiedDiffPreviewUsesEmptyRanges(t *testing.T) {
	tests := []struct {
		name string
		old  string
		new  string
		want string
	}{
		{name: "add", new: "alpha\n", want: "@@ -0,0 +1,1 @@"},
		{name: "delete", old: "alpha\n", want: "@@ -1,1 +0,0 @@"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			preview, _, _, err := unifiedDiffPreview("example.txt", test.old, test.new, 65536)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(preview, test.want) {
				t.Fatalf("preview does not contain %q:\n%s", test.want, preview)
			}
		})
	}
}

func TestUnifiedDiffPreviewTruncatesAfterCollectingStats(t *testing.T) {
	preview, truncated, stats, err := unifiedDiffPreview("example.txt", "alpha\n", "beta\n", 16)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || len([]byte(preview)) > 16 {
		t.Fatalf("preview length = %d, truncated = %v", len([]byte(preview)), truncated)
	}
	if stats != (diffStats{FilesChanged: 1, Insertions: 1, Deletions: 1}) {
		t.Fatalf("stats = %#v", stats)
	}
}

func TestUnifiedDiffPreviewReturnsEmptyForIdenticalContent(t *testing.T) {
	preview, truncated, stats, err := unifiedDiffPreview("example.txt", "same\n", "same\n", 65536)
	if err != nil {
		t.Fatal(err)
	}
	if preview != "" || truncated || stats != (diffStats{}) {
		t.Fatalf("preview = %q, truncated = %v, stats = %#v", preview, truncated, stats)
	}
}

func TestContentDiffStatsMatchesUnifiedDiffStats(t *testing.T) {
	tests := []struct {
		name string
		old  string
		new  string
	}{
		{name: "replace", old: "alpha\nkeep\n", new: "beta\nkeep\n"},
		{name: "add", old: "", new: "alpha\nbeta\n"},
		{name: "delete", old: "alpha\nbeta\n", new: ""},
		{name: "missing newline", old: "alpha", new: "beta"},
		{
			name: "repeated lines",
			old:  "same\nrepeat\nrepeat\nold\ntail\n",
			new:  "same\nrepeat\nrepeat\nnew\ntail\n",
		},
		{
			name: "moved unique anchors",
			old:  "head\na\nb\nc\ntail\n",
			new:  "head\nb\na\nc\ntail\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, want, err := unifiedDiffPreview("example.txt", test.old, test.new, 65536)
			if err != nil {
				t.Fatal(err)
			}
			if got := contentDiffStats(test.old, test.new); got != want {
				t.Fatalf("contentDiffStats() = %#v, want %#v", got, want)
			}
		})
	}
}

func TestContentDiffStatsMatchesUnifiedDiffStatsRandomized(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	vocabulary := []string{"alpha\n", "beta\n", "gamma\n", "repeat\n", "repeat\n"}
	randomContent := func() string {
		lineCount := rng.Intn(9)
		var builder strings.Builder
		for range lineCount {
			builder.WriteString(vocabulary[rng.Intn(len(vocabulary))])
		}
		content := builder.String()
		if content != "" && rng.Intn(4) == 0 {
			content = strings.TrimSuffix(content, "\n")
		}
		return content
	}

	for i := 0; i < 1000; i++ {
		oldContent := randomContent()
		newContent := randomContent()
		_, _, want, err := unifiedDiffPreview("example.txt", oldContent, newContent, 65536)
		if err != nil {
			t.Fatal(err)
		}
		if got := contentDiffStats(oldContent, newContent); got != want {
			t.Fatalf("case %d: contentDiffStats(%q, %q) = %#v, want %#v", i, oldContent, newContent, got, want)
		}
	}
}
