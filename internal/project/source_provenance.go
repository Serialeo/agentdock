package project

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	protocol "github.com/Serialeo/agentdock-protocol"
)

const sourceProvenanceTimeout = 5 * time.Second

// InspectSourceProvenance captures source-control identity without using the
// model-visible Shell capability. It is part of trusted Project context loading,
// analogous to reading AGENTS.md, and never mutates/stashes/resets/checks out the
// repository. Non-Git working folders are valid and return kind=none.
func InspectSourceProvenance(ctx context.Context, workingFolder string) (protocol.SourceProvenance, error) {
	workingFolder = filepath.Clean(strings.TrimSpace(workingFolder))
	if workingFolder == "" || !filepath.IsAbs(workingFolder) {
		return protocol.SourceProvenance{}, fmt.Errorf("source provenance working folder must be an absolute path")
	}
	markerFound, err := gitMarkerExists(workingFolder)
	if err != nil {
		return protocol.SourceProvenance{}, err
	}
	if !markerFound {
		return protocol.SourceProvenance{Kind: protocol.SourceProvenanceNone}, nil
	}

	gitPath, err := exec.LookPath("git")
	if err != nil {
		return protocol.SourceProvenance{}, fmt.Errorf("inspect Git source provenance: git executable is unavailable: %w", err)
	}
	inspectCtx, cancel := context.WithTimeout(ctx, sourceProvenanceTimeout)
	defer cancel()

	run := func(args ...string) (string, error) {
		commandArgs := append([]string{"-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false", "-C", workingFolder}, args...)
		cmd := exec.CommandContext(inspectCtx, gitPath, commandArgs...)
		cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			if inspectCtx.Err() != nil {
				return "", fmt.Errorf("git %s timed out: %w", strings.Join(args, " "), inspectCtx.Err())
			}
			message := strings.TrimSpace(stderr.String())
			if message == "" {
				message = err.Error()
			}
			return "", &gitCommandError{err: err, message: message}
		}
		return strings.TrimSpace(stdout.String()), nil
	}

	inside, err := run("rev-parse", "--is-inside-work-tree")
	if err != nil || inside != "true" {
		if err == nil {
			err = errors.New("not inside a Git worktree")
		}
		return protocol.SourceProvenance{}, fmt.Errorf("inspect Git source provenance: %w", err)
	}
	repositoryRoot, err := run("rev-parse", "--show-toplevel")
	if err != nil {
		return protocol.SourceProvenance{}, fmt.Errorf("inspect Git repository root: %w", err)
	}
	branch, branchErr := run("symbolic-ref", "--quiet", "--short", "HEAD")
	if branchErr != nil && !isGitExit(branchErr) {
		return protocol.SourceProvenance{}, fmt.Errorf("inspect Git branch: %w", branchErr)
	}
	head, headErr := run("rev-parse", "--verify", "HEAD")
	unborn := false
	detached := false
	if headErr != nil {
		if !isGitExit(headErr) || branch == "" {
			return protocol.SourceProvenance{}, fmt.Errorf("inspect Git HEAD: %w", headErr)
		}
		unborn = true
		head = ""
	} else if branchErr != nil || branch == "" {
		detached = true
		branch = ""
	}

	dirty, err := gitDirty(inspectCtx, gitPath, workingFolder)
	if err != nil {
		return protocol.SourceProvenance{}, err
	}
	provenance := protocol.SourceProvenance{
		Kind:           protocol.SourceProvenanceGit,
		RepositoryRoot: filepath.Clean(repositoryRoot),
		Head:           head,
		Branch:         branch,
		Detached:       detached,
		Unborn:         unborn,
		Dirty:          dirty,
	}
	if err := provenance.Validate(); err != nil {
		return protocol.SourceProvenance{}, fmt.Errorf("validate Git source provenance: %w", err)
	}
	return provenance, nil
}

type gitCommandError struct {
	err     error
	message string
}

func (e *gitCommandError) Error() string { return e.message }
func (e *gitCommandError) Unwrap() error { return e.err }

func isGitExit(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr)
}

func gitMarkerExists(start string) (bool, error) {
	current := filepath.Clean(start)
	for {
		_, err := os.Lstat(filepath.Join(current, ".git"))
		switch {
		case err == nil:
			info, statErr := os.Stat(filepath.Join(current, ".git"))
			if statErr != nil {
				return false, statErr
			}
			if !info.IsDir() {
				return true, nil
			}
			// 宿主可能留下空 .git 占位目录；Git 本身不会将其识别成仓库。
			entries, readErr := os.ReadDir(filepath.Join(current, ".git"))
			if readErr != nil {
				return false, readErr
			}
			if len(entries) != 0 {
				return true, nil
			}
		case errors.Is(err, os.ErrNotExist):
			// Keep walking to support a Deployment rooted at a repository subdirectory.
		default:
			return false, fmt.Errorf("inspect Git marker at %s: %w", current, err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false, nil
		}
		current = parent
	}
}

type anyOutputWriter struct{ any bool }

func (w *anyOutputWriter) Write(p []byte) (int, error) {
	if len(p) != 0 {
		w.any = true
	}
	return len(p), nil
}

func gitDirty(ctx context.Context, gitPath, workingFolder string) (bool, error) {
	cmd := exec.CommandContext(ctx, gitPath,
		"-c", "core.fsmonitor=false",
		"-c", "core.untrackedCache=false",
		"-C", workingFolder,
		"status", "--porcelain=v1", "--untracked-files=normal",
	)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	var stdout anyOutputWriter
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return false, fmt.Errorf("inspect Git dirty state timed out: %w", ctx.Err())
		}
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return false, fmt.Errorf("inspect Git dirty state: %s", message)
	}
	return stdout.any, nil
}

var _ io.Writer = (*anyOutputWriter)(nil)
