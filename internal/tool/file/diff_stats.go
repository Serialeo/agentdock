package file

import (
	"sort"
	"strings"
)

type diffLinePair struct {
	old int
	new int
}

type diffLineOccurrence struct {
	oldCount int
	newCount int
	newIndex int
}

// contentDiffStats 使用与 unifiedDiffPreview 相同的 anchored/patience matching 语义，
// 但只计算行级统计，不构造完整 unified diff。这样真实写入可以保留轻量统计，
// 同时不会因为仅用于回包的 diff 膨胀或超过 preview 上限而失败。
func contentDiffStats(oldContent, newContent string) diffStats {
	if oldContent == newContent {
		return diffStats{}
	}
	oldLines := diffLines(oldContent)
	newLines := diffLines(newContent)
	matches := anchoredDiffLineMatches(oldLines, newLines)

	done := diffLinePair{}
	stats := diffStats{FilesChanged: 1}
	for _, match := range matches {
		if match.old < done.old || match.new < done.new {
			continue
		}

		start := match
		for start.old > done.old && start.new > done.new &&
			oldLines[start.old-1] == newLines[start.new-1] {
			start.old--
			start.new--
		}

		end := match
		for end.old < len(oldLines) && end.new < len(newLines) &&
			oldLines[end.old] == newLines[end.new] {
			end.old++
			end.new++
		}

		stats.Deletions += start.old - done.old
		stats.Insertions += start.new - done.new
		done = end
	}
	return stats
}

func diffLines(content string) []string {
	lines := strings.SplitAfter(content, "\n")
	if lines[len(lines)-1] == "" {
		return lines[:len(lines)-1]
	}
	lines[len(lines)-1] += "\n\\ No newline at end of file\n"
	return lines
}

func anchoredDiffLineMatches(oldLines, newLines []string) []diffLinePair {
	occurrences := make(map[string]diffLineOccurrence, len(oldLines)+len(newLines))
	for _, line := range oldLines {
		occurrence := occurrences[line]
		occurrence.oldCount++
		occurrences[line] = occurrence
	}
	for i, line := range newLines {
		occurrence := occurrences[line]
		occurrence.newCount++
		if occurrence.newCount == 1 {
			occurrence.newIndex = i
		}
		occurrences[line] = occurrence
	}

	candidates := make([]diffLinePair, 0)
	for i, line := range oldLines {
		occurrence := occurrences[line]
		if occurrence.oldCount == 1 && occurrence.newCount == 1 {
			candidates = append(candidates, diffLinePair{old: i, new: occurrence.newIndex})
		}
	}
	anchors := longestIncreasingDiffPairs(candidates)

	matches := make([]diffLinePair, 0, len(anchors)+2)
	matches = append(matches, diffLinePair{})
	matches = append(matches, anchors...)
	matches = append(matches, diffLinePair{old: len(oldLines), new: len(newLines)})
	return matches
}

func longestIncreasingDiffPairs(pairs []diffLinePair) []diffLinePair {
	if len(pairs) == 0 {
		return nil
	}
	tails := make([]int, 0, len(pairs))
	previous := make([]int, len(pairs))
	for i := range previous {
		previous[i] = -1
	}

	for i, pair := range pairs {
		position := sort.Search(len(tails), func(j int) bool {
			return pairs[tails[j]].new >= pair.new
		})
		if position > 0 {
			previous[i] = tails[position-1]
		}
		if position == len(tails) {
			tails = append(tails, i)
		} else {
			tails[position] = i
		}
	}

	result := make([]diffLinePair, len(tails))
	current := tails[len(tails)-1]
	for i := len(result) - 1; i >= 0; i-- {
		result[i] = pairs[current]
		current = previous[current]
	}
	return result
}
