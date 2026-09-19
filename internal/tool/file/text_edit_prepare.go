package file

import "strings"

func prepareTextReplacement(path, content string, request EditRequest) (Result, string, error) {
	oldText := request.Old
	if oldText == "" {
		return nil, "", toolError("INVALID_ARGUMENT", "old is required", "validation")
	}
	newText := request.New
	expected := intValue(request.ExpectedMatches, 1)
	if expected < 0 {
		return nil, "", toolErrorDetails(
			"INVALID_EXPECTED_MATCHES",
			"expected_matches must be zero or greater",
			"validation",
			map[string]any{"expected_matches": expected},
		)
	}
	indexes := findStringIndexes(content, oldText)
	if expected > 0 && len(indexes) != expected {
		return nil, "", toolErrorDetails("MATCH_COUNT_MISMATCH", "old text matched an unexpected number of times", "validation", map[string]any{"path": path, "matches": len(indexes), "expected_matches": expected, "nearby_context": editNearbyContext(content, indexes)})
	}
	if expected == 0 && len(indexes) > 0 {
		return nil, "", toolErrorDetails("MATCH_COUNT_MISMATCH", "old text matched but expected zero matches", "validation", map[string]any{"path": path, "matches": len(indexes), "expected_matches": expected, "nearby_context": editNearbyContext(content, indexes)})
	}
	if expected > 0 && len(indexes) == 0 {
		return nil, "", toolErrorDetails("MATCH_COUNT_MISMATCH", "old text did not match", "validation", map[string]any{"path": path, "matches": 0, "expected_matches": expected})
	}

	updated := content
	if request.ReplaceAll {
		updated = strings.ReplaceAll(content, oldText, newText)
	} else {
		updated = strings.Replace(content, oldText, newText, 1)
	}
	changed := updated != content
	stats := contentDiffStats(content, updated)
	result := Result{
		"path": path, "dry_run": request.DryRun,
		"matches": len(indexes), "changed": changed,
		"files_changed": stats.FilesChanged,
		"insertions":    stats.Insertions,
		"deletions":     stats.Deletions,
		"summary":       editSummary(path, changed),
	}
	if maxDiffBytes, includePreview := diffPreviewOptions(request); includePreview {
		diffPreview, diffTruncated, _, err := unifiedDiffPreview(path, content, updated, maxDiffBytes)
		if err != nil {
			return nil, "", err
		}
		addDiffPreviewFields(result, diffPreview, diffTruncated, stats)
	}
	return result, updated, nil
}

func prepareTextAddition(path, oldContent, content string, existed bool, request EditRequest) (Result, bool, error) {
	changed := !existed || oldContent != content
	stats := contentDiffStats(oldContent, content)
	if changed && !existed && stats.FilesChanged == 0 {
		stats.FilesChanged = 1
	}
	result := Result{
		"action": "add", "path": path, "dry_run": request.DryRun,
		"changed":       changed,
		"files_changed": stats.FilesChanged,
		"insertions":    stats.Insertions,
		"deletions":     stats.Deletions,
		"summary":       editSummary(path, changed),
	}
	if maxDiffBytes, includePreview := diffPreviewOptions(request); includePreview {
		diffPreview, diffTruncated, _, err := unifiedDiffPreview(path, oldContent, content, maxDiffBytes)
		if err != nil {
			return nil, false, err
		}
		addDiffPreviewFields(result, diffPreview, diffTruncated, stats)
	}
	return result, changed, nil
}
