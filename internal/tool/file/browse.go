package file

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	DefaultBrowseLimit = 200
	MaxBrowseLimit     = 500
	MaxBrowseOffset    = 10000
	browseReadBatch    = 256
)

// BrowseDir lists only the direct children of one directory. Unlike list_dir it
// never walks descendants and never asks filepath.WalkDir to pre-sort the whole
// directory. This keeps WebUI browsing bounded even for very large directories.
func (svc *Service) BrowseDir(ctx context.Context, request BrowseRequest) (Result, error) {
	offset, limit, err := browseBounds(request.Offset, request.Limit)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	rawPath := request.Path
	if rawPath == "" {
		rawPath = "."
	}
	root, err := svc.ws.ResolveExisting(rawPath)
	if err != nil {
		return nil, err
	}
	directory, err := os.Open(root.Abs)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	rootInfo, err := directory.Stat()
	if err != nil {
		return nil, err
	}
	if !rootInfo.IsDir() {
		return nil, toolError("NOT_A_DIRECTORY", "browse path is not a directory", "validation")
	}

	items := make([]map[string]any, 0, limit)
	visibleSeen := 0
	skippedCount := 0
	truncated := false

readLoop:
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entries, readErr := directory.ReadDir(browseReadBatch)
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			fullPath := filepath.Join(root.Abs, entry.Name())
			hidden, cachedInfo, hiddenErr := browseHiddenEntry(entry)
			if hiddenErr != nil {
				if errors.Is(hiddenErr, os.ErrPermission) || errors.Is(hiddenErr, os.ErrNotExist) {
					skippedCount++
					continue
				}
				return nil, hiddenErr
			}
			if !request.IncludeHidden && hidden {
				continue
			}
			if visibleSeen < offset {
				visibleSeen++
				continue
			}
			if len(items) >= limit {
				truncated = true
				break readLoop
			}
			info := cachedInfo
			if info == nil {
				info, err = entry.Info()
				if err != nil {
					if errors.Is(err, os.ErrPermission) || errors.Is(err, os.ErrNotExist) {
						skippedCount++
						continue
					}
					return nil, err
				}
			}
			items = append(items, map[string]any{
				"name":       entry.Name(),
				"path":       fullPath,
				"type":       browseEntryKind(entry, info),
				"size_bytes": info.Size(),
				"modified":   info.ModTime().UTC().Format(time.RFC3339Nano),
				"is_hidden":  hidden,
			})
			visibleSeen++
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}

	parentPath := filepath.Dir(root.Abs)
	if filepath.Clean(parentPath) == filepath.Clean(root.Abs) {
		parentPath = ""
	}
	result := Result{
		"path":          root.Abs,
		"parent_path":   parentPath,
		"entries":       items,
		"offset":        offset,
		"limit":         limit,
		"truncated":     truncated,
		"partial":       skippedCount > 0,
		"skipped_count": skippedCount,
	}
	if truncated {
		result["next_offset"] = offset + len(items)
	}
	return result, nil
}

func browseBounds(offsetValue, limitValue *int) (int, int, error) {
	offset := intValue(offsetValue, 0)
	limit := intValue(limitValue, DefaultBrowseLimit)
	if offset < 0 || offset > MaxBrowseOffset {
		return 0, 0, toolError("INVALID_ARGUMENT", "browse offset is outside the allowed range", "validation")
	}
	if limit < 1 || limit > MaxBrowseLimit {
		return 0, 0, toolError("INVALID_ARGUMENT", "browse limit is outside the allowed range", "validation")
	}
	return offset, limit, nil
}

func browseEntryKind(entry os.DirEntry, info os.FileInfo) string {
	if entry.Type()&os.ModeSymlink != 0 || info.Mode()&os.ModeSymlink != 0 {
		return "symlink"
	}
	if info.IsDir() {
		return "directory"
	}
	if info.Mode().IsRegular() {
		return "file"
	}
	return "other"
}
