package file

import (
	"os"
	"path/filepath"
	"strings"
)

func nestedProjectPromptMarkers(root string, maxParentDepth int) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, nil
	}
	const maxEntries = 100_000
	entries := 0
	markers := make([]string, 0)
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		entries++
		if entries > maxEntries {
			return toolErrorDetails("PROJECT_PROMPT_READ_FAILED", "Project Prompt preflight exceeded the directory entry limit", "resource_limit", map[string]any{"path": root, "max_entries": maxEntries})
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		depth := 0
		if rel != "." {
			depth = strings.Count(filepath.ToSlash(rel), "/") + 1
		}
		if entry.IsDir() {
			if maxParentDepth >= 0 && depth >= maxParentDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() == "AGENTS.md" {
			markers = append(markers, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return markers, nil
}

func boundedPromptPreflightDepth(value any, fallback, minimum, maximum int) int {
	depth := fallback
	switch typed := value.(type) {
	case int:
		depth = typed
	case int32:
		depth = int(typed)
	case int64:
		depth = int(typed)
	case float64:
		depth = int(typed)
	}
	if depth < minimum {
		return minimum
	}
	if depth > maximum {
		return maximum
	}
	return depth
}

func boolPromptPreflightArg(value any) bool {
	flag, _ := value.(bool)
	return flag
}
