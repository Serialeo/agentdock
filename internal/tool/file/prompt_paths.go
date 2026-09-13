package file

import (
	"context"
	"fmt"
	"strings"
)

// ProjectPromptPaths resolves every filesystem path a structured file tool may
// touch before the handler reads or mutates file content. It is used only for
// Project Prompt scope preflight; it does not grant access to any path.
func (svc *Service) ProjectPromptPaths(ctx context.Context, tool string, args map[string]any) ([]string, error) {
	switch tool {
	case "read_file":
		path, _ := args["path"].(string)
		if strings.HasPrefix(path, "skill://") {
			return nil, nil
		}
		resolved, err := svc.ws.ResolveExistingContext(ctx, path)
		if err != nil {
			return nil, err
		}
		return []string{resolved.Abs}, nil
	case "list_dir", "search_text":
		path, _ := args["path"].(string)
		if strings.TrimSpace(path) == "" {
			path = "."
		}
		resolved, err := svc.ws.ResolveExistingContext(ctx, path)
		if err != nil {
			return nil, err
		}
		maxDepth := -1
		if tool == "list_dir" {
			maxDepth = boundedPromptPreflightDepth(args["max_depth"], 1, 1, 20)
		}
		markers, err := nestedProjectPromptMarkers(resolved.Abs, maxDepth)
		if err != nil {
			return nil, err
		}
		return uniqueAbsolutePaths(append([]string{resolved.Abs}, markers...)...), nil
	case "file_edit":
		return svc.projectPromptEditPaths(ctx, args)
	default:
		return nil, nil
	}
}

func (svc *Service) projectPromptEditPaths(ctx context.Context, args map[string]any) ([]string, error) {
	action, _ := args["action"].(string)
	action = strings.ToLower(strings.TrimSpace(action))
	path, _ := args["path"].(string)
	newPath, _ := args["new_path"].(string)
	switch action {
	case "replace", "delete":
		resolved, err := svc.ws.ResolveExistingContext(ctx, path)
		if err != nil {
			return nil, err
		}
		paths := []string{resolved.Abs}
		if action == "delete" && boolPromptPreflightArg(args["recursive"]) {
			markers, scanErr := nestedProjectPromptMarkers(resolved.Abs, -1)
			if scanErr != nil {
				return nil, scanErr
			}
			paths = append(paths, markers...)
		}
		return uniqueAbsolutePaths(paths...), nil
	case "add":
		resolved, err := svc.ws.ResolveForWriteContext(ctx, path)
		if err != nil {
			return nil, err
		}
		return []string{resolved.Abs}, nil
	case "move":
		source, err := svc.ws.ResolveExistingContext(ctx, path)
		if err != nil {
			return nil, err
		}
		destination, err := svc.ws.ResolveForWriteContext(ctx, newPath)
		if err != nil {
			return nil, err
		}
		paths := []string{source.Abs, destination.Abs}
		markers, scanErr := nestedProjectPromptMarkers(source.Abs, -1)
		if scanErr != nil {
			return nil, scanErr
		}
		paths = append(paths, markers...)
		return uniqueAbsolutePaths(paths...), nil
	case "patch":
		patch, _ := args["patch"].(string)
		workdirRaw, _ := args["workdir"].(string)
		workdir, err := svc.patchWorkdir(ctx, workdirRaw)
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(strings.TrimSpace(patch), "*** Begin Patch") {
			operations, parseErr := parseEnvelopePatch(patch)
			if parseErr != nil {
				return nil, parseErr
			}
			paths := make([]string, 0, len(operations)*2)
			for _, operation := range operations {
				operationPath, pathErr := patchPathInBase(workdir.Abs, operation.Path)
				if pathErr != nil {
					return nil, pathErr
				}
				switch operation.Kind {
				case "add":
					resolved, resolveErr := svc.ws.ResolveForWriteContext(ctx, operationPath)
					if resolveErr != nil {
						return nil, resolveErr
					}
					paths = append(paths, resolved.Abs)
				case "delete", "update":
					resolved, resolveErr := svc.ws.ResolveExistingContext(ctx, operationPath)
					if resolveErr != nil {
						return nil, resolveErr
					}
					paths = append(paths, resolved.Abs)
				default:
					return nil, toolErrorDetails("PATCH_FAILED", "unsupported patch operation during Project Prompt preflight", "validation", map[string]any{"operation": operation.Kind})
				}
				if strings.TrimSpace(operation.MoveTo) != "" {
					movePath, moveErr := patchPathInBase(workdir.Abs, operation.MoveTo)
					if moveErr != nil {
						return nil, moveErr
					}
					resolved, resolveErr := svc.ws.ResolveForWriteContext(ctx, movePath)
					if resolveErr != nil {
						return nil, resolveErr
					}
					paths = append(paths, resolved.Abs)
				}
			}
			return uniqueAbsolutePaths(paths...), nil
		}

		files := parseDiffFiles(patch)
		if len(files) == 0 {
			return nil, toolError("PATCH_FAILED", "cannot determine affected files for Project Prompt preflight", "validation")
		}
		paths := make([]string, 0, len(files))
		for _, file := range files {
			if strings.TrimSpace(file.Path) == "" {
				return nil, toolError("PATCH_FAILED", "diff contains an empty file path", "validation")
			}
			candidate, pathErr := patchPathInBase(workdir.Abs, file.Path)
			if pathErr != nil {
				return nil, pathErr
			}
			var resolvedPath string
			if file.Status == "added" {
				resolved, resolveErr := svc.ws.ResolveForWriteContext(ctx, candidate)
				if resolveErr != nil {
					return nil, resolveErr
				}
				resolvedPath = resolved.Abs
			} else {
				resolved, resolveErr := svc.ws.ResolveExistingContext(ctx, candidate)
				if resolveErr != nil {
					return nil, resolveErr
				}
				resolvedPath = resolved.Abs
			}
			paths = append(paths, resolvedPath)
		}
		return uniqueAbsolutePaths(paths...), nil
	default:
		return nil, fmt.Errorf("unsupported file_edit action during Project Prompt preflight: %s", action)
	}
}

func uniqueAbsolutePaths(values ...string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
