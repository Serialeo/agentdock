package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	protocol "github.com/Serialeo/agentdock-protocol"
	projectstate "github.com/uvwt/agentdock/internal/project"
)

func (r *Runtime) ensureProjectPromptForTool(ctx context.Context, name string, args map[string]any) error {
	execution, scoped := projectstate.ExecutionFromContext(ctx)
	if !scoped || r.projectInstructions == nil {
		return nil
	}
	paths, err := r.projectPromptPathsForTool(ctx, name, args)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return nil
	}
	return r.ensureProjectPromptPaths(execution, paths, name)
}

func (r *Runtime) projectPromptPathsForTool(ctx context.Context, name string, args map[string]any) ([]string, error) {
	switch name {
	case "read_file", "list_dir", "search_text", "file_edit":
		return r.files.ProjectPromptPaths(ctx, name, args)
	case "view_image":
		path, _ := args["path"].(string)
		if strings.TrimSpace(path) == "" {
			return nil, nil
		}
		resolved, err := r.ws.ResolveExistingContext(ctx, path)
		if err != nil {
			return nil, err
		}
		return []string{resolved.Abs}, nil
	case "exec_command":
		workdir, _ := args["workdir"].(string)
		if strings.TrimSpace(workdir) == "" {
			return nil, nil
		}
		resolved, err := r.ws.ResolveExistingContext(ctx, workdir)
		if err != nil {
			return nil, err
		}
		return []string{resolved.Abs}, nil
	case "acp_session":
		action, _ := args["action"].(string)
		action = strings.ToLower(strings.TrimSpace(action))
		if action != "new" && action != "fork" {
			return nil, nil
		}
		paths := make([]string, 0, 4)
		if cwd, _ := args["cwd"].(string); strings.TrimSpace(cwd) != "" {
			resolved, err := r.ws.ResolveExistingContext(ctx, cwd)
			if err != nil {
				return nil, err
			}
			paths = append(paths, resolved.Abs)
		}
		for _, raw := range stringSliceArgument(args["additional_directories"]) {
			resolved, err := r.ws.ResolveExistingContext(ctx, raw)
			if err != nil {
				return nil, err
			}
			paths = append(paths, resolved.Abs)
		}
		return paths, nil
	default:
		return nil, nil
	}
}

func (r *Runtime) ensureProjectPromptPaths(execution projectstate.Execution, paths []string, tool string) error {
	if strings.TrimSpace(execution.Deployment.WorkingFolder) == "" {
		// A folderless Deployment deliberately has no Project Prompt discovery root.
		// Full Access and absolute Host paths therefore never cause HOME/system/other
		// directories to become implicit AGENTS.md sources.
		return nil
	}
	realRoot, err := filepath.EvalSymlinks(execution.Deployment.WorkingFolder)
	if err != nil {
		return &protocol.RemoteError{Code: protocol.ErrorDeploymentNotReady, Message: "cannot resolve Deployment working_folder for Project Prompt preflight", Category: "runtime", Details: map[string]any{"reason": err.Error()}}
	}
	missing := make([]map[string]any, 0)
	seen := make(map[string]struct{})
	for _, path := range paths {
		scope, inside, scopeErr := promptScopeForResolvedPath(realRoot, path)
		if scopeErr != nil {
			return scopeErr
		}
		if !inside {
			// Host paths outside the Deployment keep the current Project guidance but
			// never cause automatic discovery of external/HOME/system AGENTS.md.
			continue
		}
		loaded, loadErr := r.projectInstructions.Load(execution.Deployment, scope)
		if loadErr != nil {
			return loadErr
		}
		if promptRevisionDelivered(execution.Target.PromptScopes, loaded.Prompt.PromptRevision) {
			continue
		}
		key := loaded.CWDRel + "\x00" + loaded.Prompt.PromptRevision
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		missing = append(missing, map[string]any{"scope": loaded.CWDRel})
	}
	if len(missing) == 0 {
		return nil
	}
	return &protocol.RemoteError{
		Code: protocol.ErrorContextRefreshRequired, Message: "Project Prompt context must be refreshed before this tool can access the requested scope", Category: "conflict",
		Details: map[string]any{"tool": tool, "target_id": execution.Target.TargetID, "missing_prompt_scopes": missing},
	}
}

func promptScopeForResolvedPath(realRoot, path string) (string, bool, error) {
	path = filepath.Clean(path)
	rel, err := filepath.Rel(realRoot, path)
	if err != nil {
		return "", false, err
	}
	if projectPathEscapes(rel) {
		return "", false, nil
	}
	scopeAbs := path
	info, statErr := os.Stat(scopeAbs)
	if statErr == nil && !info.IsDir() {
		scopeAbs = filepath.Dir(scopeAbs)
	} else if errors.Is(statErr, os.ErrNotExist) {
		scopeAbs = filepath.Dir(scopeAbs)
	} else if statErr != nil {
		return "", false, statErr
	}
	for {
		info, statErr = os.Stat(scopeAbs)
		if statErr == nil && info.IsDir() {
			break
		}
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return "", false, statErr
		}
		parent := filepath.Dir(scopeAbs)
		if parent == scopeAbs {
			return "", false, &protocol.RemoteError{Code: protocol.ErrorProjectPromptReadFailed, Message: "cannot find an existing directory for Project Prompt scope", Category: "runtime", Details: map[string]any{"path": path}}
		}
		scopeAbs = parent
	}
	scopeRel, err := filepath.Rel(realRoot, scopeAbs)
	if err != nil {
		return "", false, err
	}
	if projectPathEscapes(scopeRel) {
		return "", false, nil
	}
	if scopeRel == "." {
		return ".", true, nil
	}
	return filepath.ToSlash(scopeRel), true, nil
}

func projectPathEscapes(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel)
}

func stringSliceArgument(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}
