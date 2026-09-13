package app

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	skills "github.com/uvwt/agentdock/internal/skill"
	"golang.org/x/text/unicode/norm"
)

const (
	commonSkillIndexLimit       = 50
	commonSkillDescriptionBytes = 120
)

func commonSkillCapabilityIndex(installed []capabilitySkillItem) (*capabilityCommonSkillIndex, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	root := filepath.Join(home, ".agents", "skills")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return &capabilityCommonSkillIndex{Root: root, Items: []capabilityCommonSkillItem{}}, nil
		}
		return nil, err
	}

	items := make([]capabilityCommonSkillItem, 0, len(entries))
	for _, entry := range entries {
		packageDir := filepath.Join(root, entry.Name())
		info, statErr := os.Stat(packageDir)
		if statErr != nil || !info.IsDir() {
			continue
		}
		documentPath := filepath.Join(packageDir, "SKILL.md")
		data, readErr := os.ReadFile(documentPath)
		if readErr != nil {
			continue
		}
		metadata, parseErr := skills.ParseSkillMetadata(data)
		if parseErr != nil {
			continue
		}
		items = append(items, capabilityCommonSkillItem{
			Name:        metadata.Name,
			Description: truncateString(strings.TrimSpace(metadata.Description), commonSkillDescriptionBytes),
			File:        documentPath,
		})
	}

	total := len(items)
	installedNames := make(map[string]struct{}, len(installed))
	for _, item := range installed {
		if key := normalizedSkillName(item.Name); key != "" {
			installedNames[key] = struct{}{}
		}
	}

	byName := make(map[string]capabilityCommonSkillItem, len(items))
	shadowed := 0
	for _, item := range items {
		key := normalizedSkillName(item.Name)
		if _, exists := installedNames[key]; exists {
			shadowed++
			continue
		}
		if existing, exists := byName[key]; exists {
			shadowed++
			// Multiple common packages can declare the same normalized name. Pick the stable
			// lexicographically first package path instead of asking the model to resolve it.
			if item.File < existing.File {
				byName[key] = item
			}
			continue
		}
		byName[key] = item
	}
	filtered := make([]capabilityCommonSkillItem, 0, len(byName))
	for _, item := range byName {
		filtered = append(filtered, item)
	}

	// 文件系统遍历顺序不应影响启动 Context；先完成 installed/common 冲突过滤，
	// 再按规范化名称和路径稳定排序并应用预算。
	sort.Slice(filtered, func(i, j int) bool {
		left, right := normalizedSkillName(filtered[i].Name), normalizedSkillName(filtered[j].Name)
		if left == right {
			return filtered[i].File < filtered[j].File
		}
		return left < right
	})
	effective := len(filtered)
	truncated := effective > commonSkillIndexLimit
	if truncated {
		filtered = filtered[:commonSkillIndexLimit]
	}
	return &capabilityCommonSkillIndex{Root: root, Total: total, Effective: effective, Shadowed: shadowed, Truncated: truncated, Items: filtered}, nil
}

func normalizedSkillName(value string) string {
	return strings.ToLower(norm.NFKC.String(strings.TrimSpace(value)))
}
