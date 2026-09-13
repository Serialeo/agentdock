package instructions

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	protocol "github.com/Serialeo/agentdock-protocol"
	"github.com/uvwt/agentdock/internal/fs/atomicfile"
)

const agentsFileName = "AGENTS.md"

type Loader struct {
	defaultRoot string
	writeMu     sync.Mutex
	afterRead   func(path string, attempt int)
}

func NewLoader(defaultRoot ...string) *Loader {
	loader := &Loader{}
	if len(defaultRoot) > 0 {
		loader.defaultRoot = strings.TrimSpace(defaultRoot[0])
	}
	return loader
}

func (l *Loader) Load(deployment protocol.Deployment, cwdRel string) (protocol.ProjectPromptLoadResult, error) {
	if strings.TrimSpace(deployment.ID) == "" || strings.TrimSpace(deployment.AppliedRevision) == "" {
		return protocol.ProjectPromptLoadResult{}, promptError(protocol.ErrorExecutionContextInvalid, "Deployment identity is incomplete for Project Prompt loading", "validation", nil)
	}
	configuredRoot := strings.TrimSpace(deployment.WorkingFolder)
	root := configuredRoot
	if root == "" {
		root = l.defaultRoot
	}
	if strings.TrimSpace(root) == "" {
		return protocol.ProjectPromptLoadResult{}, promptError(protocol.ErrorDeploymentNotReady, "Node default working directory is unavailable", "runtime", nil)
	}
	realRoot, err := canonicalProjectRoot(root)
	if err != nil {
		return protocol.ProjectPromptLoadResult{}, err
	}
	normalizedScope, cwdAbs, err := resolveProjectScope(realRoot, cwdRel)
	if err != nil {
		return protocol.ProjectPromptLoadResult{}, err
	}
	if configuredRoot == "" {
		return protocol.ProjectPromptLoadResult{
			DeploymentID: deployment.ID,
			CWDRel:       normalizedScope,
			Prompt: protocol.ProjectPrompt{
				PromptRevision: promptRevision(nil), Complete: true, Bytes: 0, Sources: []protocol.PromptSource{},
			},
		}, nil
	}

	dirs, err := scopeDirectories(realRoot, cwdAbs)
	if err != nil {
		return protocol.ProjectPromptLoadResult{}, err
	}
	sources := make([]protocol.PromptSource, 0, len(dirs))
	total := 0
	for _, dir := range dirs {
		source, exists, readErr := l.readSource(realRoot, dir)
		if readErr != nil {
			return protocol.ProjectPromptLoadResult{}, readErr
		}
		if !exists {
			continue
		}
		total += source.Bytes
		if total > protocol.MaxProjectPromptTargetBytes {
			return protocol.ProjectPromptLoadResult{}, promptError(
				protocol.ErrorProjectPromptTooLarge,
				"applicable Project Prompt exceeds the per-Target byte budget",
				"resource_limit",
				map[string]any{"bytes": total, "max_bytes": protocol.MaxProjectPromptTargetBytes, "path": source.Path},
			)
		}
		sources = append(sources, source)
	}
	prompt := protocol.ProjectPrompt{
		PromptRevision: promptRevision(sources),
		Complete:       true,
		Bytes:          total,
		Sources:        sources,
	}
	return protocol.ProjectPromptLoadResult{DeploymentID: deployment.ID, CWDRel: normalizedScope, Prompt: prompt}, nil
}

func (l *Loader) Write(deployment protocol.Deployment, request protocol.ProjectPromptWriteRequest) (protocol.ProjectPromptWriteResult, error) {
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	if strings.TrimSpace(deployment.WorkingFolder) == "" {
		return protocol.ProjectPromptWriteResult{}, promptError(protocol.ErrorDeploymentNotReady, "Project Prompt requires a configured Project folder", "validation", map[string]any{"deployment_id": deployment.ID})
	}

	if request.DeploymentID != deployment.ID || request.DeploymentRevision != deployment.AppliedRevision {
		return protocol.ProjectPromptWriteResult{}, promptError(protocol.ErrorRevisionConflict, "Deployment revision is stale for Project Prompt write", "conflict", map[string]any{"deployment_id": request.DeploymentID, "applied_revision": deployment.AppliedRevision})
	}
	content := []byte(request.Content)
	if !utf8.Valid(content) {
		return protocol.ProjectPromptWriteResult{}, promptError(protocol.ErrorProjectPromptReadFailed, "Project Prompt source content must be valid UTF-8", "validation", nil)
	}
	if len(content) > protocol.MaxProjectPromptFileBytes {
		return protocol.ProjectPromptWriteResult{}, promptError(protocol.ErrorProjectPromptTooLarge, "Project Prompt source exceeds the per-file byte budget", "resource_limit", map[string]any{"bytes": len(content), "max_bytes": protocol.MaxProjectPromptFileBytes})
	}

	realRoot, err := canonicalProjectRoot(deployment.WorkingFolder)
	if err != nil {
		return protocol.ProjectPromptWriteResult{}, err
	}
	normalizedScope, scopeAbs, err := resolveProjectScope(realRoot, request.Scope)
	if err != nil {
		return protocol.ProjectPromptWriteResult{}, err
	}
	current, exists, err := l.readSource(realRoot, scopeAbs)
	if err != nil {
		return protocol.ProjectPromptWriteResult{}, err
	}
	if request.Create {
		if exists {
			return protocol.ProjectPromptWriteResult{}, promptError(protocol.ErrorRevisionConflict, "Project Prompt source already exists", "conflict", map[string]any{"path": current.Path, "current_sha256": current.SHA256})
		}
		if strings.TrimSpace(request.ExpectedSHA256) != "" {
			return protocol.ProjectPromptWriteResult{}, promptError(protocol.ErrorRevisionConflict, "create request must not provide an existing source hash", "conflict", map[string]any{"expected_sha256": request.ExpectedSHA256})
		}
	} else {
		if !exists {
			return protocol.ProjectPromptWriteResult{}, promptError(protocol.ErrorRevisionConflict, "Project Prompt source no longer exists", "conflict", map[string]any{"scope": normalizedScope})
		}
		if strings.TrimSpace(request.ExpectedSHA256) == "" || request.ExpectedSHA256 != current.SHA256 {
			return protocol.ProjectPromptWriteResult{}, promptError(protocol.ErrorRevisionConflict, "Project Prompt source changed since it was loaded", "conflict", map[string]any{"path": current.Path, "expected_sha256": request.ExpectedSHA256, "current_sha256": current.SHA256})
		}
	}

	target := filepath.Join(scopeAbs, agentsFileName)
	mode := os.FileMode(0o600)
	if exists {
		info, statErr := os.Lstat(target)
		if statErr != nil {
			return protocol.ProjectPromptWriteResult{}, sourceReadError(realRoot, target, "stat Project Prompt source before write", statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return protocol.ProjectPromptWriteResult{}, promptError(protocol.ErrorProjectPromptReadFailed, "Project Prompt source must remain a regular file", "validation", map[string]any{"path": displayProjectPath(realRoot, target)})
		}
		if info.Mode().Perm() != 0 {
			mode = info.Mode().Perm()
		}
	}
	if err := atomicfile.Write(target, content, mode); err != nil {
		return protocol.ProjectPromptWriteResult{}, sourceReadError(realRoot, target, "write Project Prompt source", err)
	}

	loaded, err := l.Load(deployment, normalizedScope)
	if err != nil {
		return protocol.ProjectPromptWriteResult{}, err
	}
	var written protocol.PromptSource
	found := false
	for _, source := range loaded.Prompt.Sources {
		if source.Scope == normalizedScope {
			written = source
			found = true
		}
	}
	if !found {
		return protocol.ProjectPromptWriteResult{}, promptError(protocol.ErrorProjectPromptReadFailed, "written Project Prompt source was not present after reload", "runtime", map[string]any{"scope": normalizedScope})
	}
	return protocol.ProjectPromptWriteResult{DeploymentID: deployment.ID, Scope: normalizedScope, Source: written, Prompt: loaded.Prompt, Created: request.Create}, nil
}

func canonicalProjectRoot(raw string) (string, error) {
	root := filepath.Clean(strings.TrimSpace(raw))
	if !filepath.IsAbs(root) {
		return "", promptError(protocol.ErrorExecutionContextInvalid, "Deployment working_folder must be absolute", "validation", map[string]any{"working_folder": raw})
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", promptError(protocol.ErrorDeploymentNotReady, "cannot resolve Deployment working_folder for Project Prompt", "runtime", map[string]any{"working_folder": raw, "reason": err.Error()})
	}
	info, err := os.Stat(realRoot)
	if err != nil || !info.IsDir() {
		details := map[string]any{"working_folder": raw}
		if err != nil {
			details["reason"] = err.Error()
		}
		return "", promptError(protocol.ErrorDeploymentNotReady, "Deployment working_folder is not an available directory", "runtime", details)
	}
	return realRoot, nil
}

func resolveProjectScope(realRoot, raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "."
	}
	if filepath.IsAbs(raw) || strings.HasPrefix(raw, "~") {
		return "", "", promptError(protocol.ErrorPromptScopeEscape, "Project Prompt scope must be relative to the Deployment working_folder", "validation", map[string]any{"cwd_rel": raw})
	}
	clean := filepath.Clean(filepath.FromSlash(raw))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", "", promptError(protocol.ErrorPromptScopeEscape, "Project Prompt scope escapes the Deployment working_folder", "validation", map[string]any{"cwd_rel": raw})
	}
	candidate := filepath.Join(realRoot, clean)
	realCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", "", promptError(protocol.ErrorProjectPromptReadFailed, "Project Prompt scope is unavailable", "runtime", map[string]any{"cwd_rel": raw, "reason": err.Error()})
	}
	rel, err := filepath.Rel(realRoot, realCandidate)
	if err != nil || pathEscapes(rel) {
		return "", "", promptError(protocol.ErrorPromptScopeEscape, "Project Prompt scope resolves outside the Deployment working_folder", "authorization", map[string]any{"cwd_rel": raw})
	}
	info, err := os.Stat(realCandidate)
	if err != nil || !info.IsDir() {
		details := map[string]any{"cwd_rel": raw}
		if err != nil {
			details["reason"] = err.Error()
		}
		return "", "", promptError(protocol.ErrorProjectPromptReadFailed, "Project Prompt scope is not an available directory", "runtime", details)
	}
	return slashRel(rel), realCandidate, nil
}

func scopeDirectories(root, cwd string) ([]string, error) {
	rel, err := filepath.Rel(root, cwd)
	if err != nil || pathEscapes(rel) {
		return nil, promptError(protocol.ErrorPromptScopeEscape, "Project Prompt directory chain escapes the Deployment working_folder", "authorization", nil)
	}
	dirs := []string{root}
	if rel == "." {
		return dirs, nil
	}
	current := root
	for _, part := range strings.Split(filepath.Clean(rel), string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		dirs = append(dirs, current)
	}
	return dirs, nil
}

func (l *Loader) readSource(root, dir string) (protocol.PromptSource, bool, error) {
	candidate := filepath.Join(dir, agentsFileName)
	for attempt := 0; attempt < 2; attempt++ {
		before, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			return protocol.PromptSource{}, false, nil
		}
		if err != nil {
			return protocol.PromptSource{}, false, sourceReadError(root, candidate, "stat Project Prompt source", err)
		}
		if before.Mode()&os.ModeSymlink != 0 {
			resolved, resolveErr := filepath.EvalSymlinks(candidate)
			if resolveErr != nil {
				return protocol.PromptSource{}, false, sourceReadError(root, candidate, "resolve Project Prompt source symlink", resolveErr)
			}
			if !insideRoot(root, resolved) {
				return protocol.PromptSource{}, false, promptError(protocol.ErrorPromptScopeEscape, "Project Prompt source symlink escapes the Deployment working_folder", "authorization", map[string]any{"path": displayProjectPath(root, candidate)})
			}
			return protocol.PromptSource{}, false, promptError(protocol.ErrorProjectPromptReadFailed, "Project Prompt source must be a regular file, not a symlink", "validation", map[string]any{"path": displayProjectPath(root, candidate)})
		}
		if !before.Mode().IsRegular() {
			return protocol.PromptSource{}, false, promptError(protocol.ErrorProjectPromptReadFailed, "Project Prompt source is not a regular file", "validation", map[string]any{"path": displayProjectPath(root, candidate)})
		}
		if before.Size() > protocol.MaxProjectPromptFileBytes {
			return protocol.PromptSource{}, false, promptError(protocol.ErrorProjectPromptTooLarge, "Project Prompt source exceeds the per-file byte budget", "resource_limit", map[string]any{"path": displayProjectPath(root, candidate), "bytes": before.Size(), "max_bytes": protocol.MaxProjectPromptFileBytes})
		}

		file, openErr := os.Open(candidate)
		if openErr != nil {
			return protocol.PromptSource{}, false, sourceReadError(root, candidate, "open Project Prompt source", openErr)
		}
		opened, statErr := file.Stat()
		if statErr != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
			_ = file.Close()
			if attempt == 0 {
				continue
			}
			return protocol.PromptSource{}, false, promptError(protocol.ErrorProjectPromptReadFailed, "Project Prompt source changed while opening", "conflict", map[string]any{"path": displayProjectPath(root, candidate)})
		}
		data, readErr := io.ReadAll(io.LimitReader(file, int64(protocol.MaxProjectPromptFileBytes)+1))
		afterFD, afterStatErr := file.Stat()
		closeErr := file.Close()
		if readErr != nil {
			return protocol.PromptSource{}, false, sourceReadError(root, candidate, "read Project Prompt source", readErr)
		}
		if closeErr != nil {
			return protocol.PromptSource{}, false, sourceReadError(root, candidate, "close Project Prompt source", closeErr)
		}
		if len(data) > protocol.MaxProjectPromptFileBytes {
			return protocol.PromptSource{}, false, promptError(protocol.ErrorProjectPromptTooLarge, "Project Prompt source exceeds the per-file byte budget", "resource_limit", map[string]any{"path": displayProjectPath(root, candidate), "bytes": len(data), "max_bytes": protocol.MaxProjectPromptFileBytes})
		}
		if l.afterRead != nil {
			l.afterRead(candidate, attempt)
		}
		afterPath, pathStatErr := os.Lstat(candidate)
		stable := afterStatErr == nil && pathStatErr == nil && afterPath.Mode().IsRegular() &&
			os.SameFile(opened, afterFD) && os.SameFile(opened, afterPath) &&
			opened.Size() == afterFD.Size() && opened.ModTime().Equal(afterFD.ModTime()) &&
			afterPath.Size() == afterFD.Size() && afterPath.ModTime().Equal(afterFD.ModTime()) &&
			afterFD.Size() == int64(len(data))
		if !stable {
			if attempt == 0 {
				continue
			}
			return protocol.PromptSource{}, false, promptError(protocol.ErrorProjectPromptReadFailed, "Project Prompt source changed while being read", "conflict", map[string]any{"path": displayProjectPath(root, candidate)})
		}
		if !utf8.Valid(data) {
			return protocol.PromptSource{}, false, promptError(protocol.ErrorProjectPromptReadFailed, "Project Prompt source is not valid UTF-8", "validation", map[string]any{"path": displayProjectPath(root, candidate)})
		}
		scope := displayProjectPath(root, dir)
		path := displayProjectPath(root, candidate)
		hash := sha256.Sum256(data)
		return protocol.PromptSource{
			Path: path, Scope: scope, SHA256: fmt.Sprintf("sha256:%x", hash), Bytes: len(data), Content: string(data),
		}, true, nil
	}
	return protocol.PromptSource{}, false, promptError(protocol.ErrorProjectPromptReadFailed, "Project Prompt source could not be read consistently", "conflict", map[string]any{"path": displayProjectPath(root, candidate)})
}

func promptRevision(sources []protocol.PromptSource) string {
	h := sha256.New()
	_, _ = io.WriteString(h, "agentdock-project-prompt-v1\x00")
	for _, source := range sources {
		_, _ = io.WriteString(h, source.Path)
		_, _ = h.Write([]byte{0})
		_, _ = io.WriteString(h, source.Scope)
		_, _ = h.Write([]byte{0})
		_, _ = io.WriteString(h, source.SHA256)
		_, _ = h.Write([]byte{0})
		_, _ = io.WriteString(h, strconv.Itoa(source.Bytes))
		_, _ = h.Write([]byte{0})
	}
	return fmt.Sprintf("sha256:%x", h.Sum(nil))
}

func sourceReadError(root, path, action string, err error) *protocol.RemoteError {
	return promptError(protocol.ErrorProjectPromptReadFailed, action, "runtime", map[string]any{"path": displayProjectPath(root, path), "reason": err.Error()})
}

func promptError(code, message, category string, details map[string]any) *protocol.RemoteError {
	return &protocol.RemoteError{Code: code, Message: message, Category: category, Details: details}
}

func displayProjectPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || pathEscapes(rel) {
		return filepath.Clean(path)
	}
	return slashRel(rel)
}

func slashRel(rel string) string {
	if rel == "" || rel == "." {
		return "."
	}
	return filepath.ToSlash(rel)
}

func pathEscapes(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel)
}

func insideRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && !pathEscapes(rel)
}
