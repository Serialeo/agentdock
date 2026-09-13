package project

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	protocol "github.com/Serialeo/agentdock-protocol"
)

func TestInspectSourceProvenanceNonGit(t *testing.T) {
	provenance, err := InspectSourceProvenance(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if provenance != (protocol.SourceProvenance{Kind: protocol.SourceProvenanceNone}) {
		t.Fatalf("non-Git provenance = %#v", provenance)
	}
}

func TestInspectSourceProvenanceGitStates(t *testing.T) {
	requireGitForProvenanceTest(t)

	t.Run("clean_branch_and_subdirectory", func(t *testing.T) {
		repo := initGitRepository(t, true)
		subdir := filepath.Join(repo, "nested", "work")
		if err := os.MkdirAll(subdir, 0o700); err != nil {
			t.Fatal(err)
		}
		provenance, err := InspectSourceProvenance(context.Background(), subdir)
		if err != nil {
			t.Fatal(err)
		}
		if provenance.Kind != protocol.SourceProvenanceGit || provenance.RepositoryRoot != canonicalTestPath(t, repo) || provenance.Head == "" || provenance.Branch == "" || provenance.Detached || provenance.Unborn || provenance.Dirty {
			t.Fatalf("clean branch provenance = %#v", provenance)
		}
	})

	t.Run("dirty", func(t *testing.T) {
		repo := initGitRepository(t, true)
		if err := os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("dirty\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		provenance, err := InspectSourceProvenance(context.Background(), repo)
		if err != nil {
			t.Fatal(err)
		}
		if !provenance.Dirty || provenance.Head == "" || provenance.Branch == "" {
			t.Fatalf("dirty provenance = %#v", provenance)
		}
	})

	t.Run("detached", func(t *testing.T) {
		repo := initGitRepository(t, true)
		runGitForProvenanceTest(t, repo, "checkout", "--detach", "HEAD")
		provenance, err := InspectSourceProvenance(context.Background(), repo)
		if err != nil {
			t.Fatal(err)
		}
		if !provenance.Detached || provenance.Branch != "" || provenance.Head == "" || provenance.Unborn {
			t.Fatalf("detached provenance = %#v", provenance)
		}
	})

	t.Run("unborn", func(t *testing.T) {
		repo := initGitRepository(t, false)
		if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("unborn\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		provenance, err := InspectSourceProvenance(context.Background(), repo)
		if err != nil {
			t.Fatal(err)
		}
		if !provenance.Unborn || provenance.Head != "" || provenance.Branch == "" || provenance.Detached || !provenance.Dirty {
			t.Fatalf("unborn provenance = %#v", provenance)
		}
	})
}

func TestInspectSourceProvenanceDoesNotSilentlyDowngradeBrokenGitRepository(t *testing.T) {
	requireGitForProvenanceTest(t)
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".git"), []byte("gitdir: missing-worktree-metadata\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if provenance, err := InspectSourceProvenance(context.Background(), repo); err == nil {
		t.Fatalf("broken Git repository was silently accepted as %#v", provenance)
	}
}

func requireGitForProvenanceTest(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
}

func initGitRepository(t *testing.T, commit bool) string {
	t.Helper()
	repo := t.TempDir()
	runGitForProvenanceTest(t, repo, "init")
	runGitForProvenanceTest(t, repo, "symbolic-ref", "HEAD", "refs/heads/main")
	if !commit {
		return repo
	}
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("tracked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitForProvenanceTest(t, repo, "add", "tracked.txt")
	runGitForProvenanceTest(t, repo, "-c", "user.name=AgentDock Test", "-c", "user.email=agentdock@example.invalid", "commit", "-m", "initial")
	return filepath.Clean(repo)
}

func runGitForProvenanceTest(t *testing.T, repo string, args ...string) string {
	t.Helper()
	commandArgs := append([]string{"-C", repo}, args...)
	output, err := exec.Command("git", commandArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}
