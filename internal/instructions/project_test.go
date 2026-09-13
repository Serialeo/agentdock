package instructions

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	protocol "github.com/Serialeo/agentdock-protocol"
)

func TestProjectPromptLoadsRootToNestedChainOnly(t *testing.T) {
	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, "backend", "auth"))
	mustMkdirAll(t, filepath.Join(root, "frontend"))
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "root rules\n")
	mustWrite(t, filepath.Join(root, "backend", "AGENTS.md"), "backend rules\n")
	mustWrite(t, filepath.Join(root, "backend", "auth", "AGENTS.md"), "auth rules\n")
	mustWrite(t, filepath.Join(root, "frontend", "AGENTS.md"), "frontend sibling must not load\n")

	home := t.TempDir()
	t.Setenv("HOME", home)
	mustWrite(t, filepath.Join(home, "AGENTS.md"), "home rules must not load\n")
	parent := filepath.Dir(root)
	outsideParent := filepath.Join(parent, "AGENTS.md")
	if _, err := os.Stat(outsideParent); errors.Is(err, os.ErrNotExist) {
		mustWrite(t, outsideParent, "parent rules must not load\n")
		t.Cleanup(func() { _ = os.Remove(outsideParent) })
	}

	result, err := NewLoader().Load(testDeployment(root), "backend/auth")
	if err != nil {
		t.Fatal(err)
	}
	if result.CWDRel != "backend/auth" || !result.Prompt.Complete {
		t.Fatalf("prompt result = %#v", result)
	}
	if len(result.Prompt.Sources) != 3 {
		t.Fatalf("sources = %#v, want root/backend/auth only", result.Prompt.Sources)
	}
	wantPaths := []string{"AGENTS.md", "backend/AGENTS.md", "backend/auth/AGENTS.md"}
	wantScopes := []string{".", "backend", "backend/auth"}
	for i, source := range result.Prompt.Sources {
		if source.Path != wantPaths[i] || source.Scope != wantScopes[i] || source.Content == "" || source.Bytes != len(source.Content) || !strings.HasPrefix(source.SHA256, "sha256:") {
			t.Fatalf("source[%d] = %#v", i, source)
		}
	}
	combined := ""
	for _, source := range result.Prompt.Sources {
		combined += source.Content
	}
	if strings.Contains(combined, "frontend") || strings.Contains(combined, "home rules") || strings.Contains(combined, "parent rules") {
		t.Fatalf("unrelated rules leaked into Project Prompt: %q", combined)
	}
}

func TestProjectPromptMissingRulesIsCompleteEmptyChain(t *testing.T) {
	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, "src"))
	result, err := NewLoader().Load(testDeployment(root), "src")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Prompt.Complete || result.Prompt.Bytes != 0 || len(result.Prompt.Sources) != 0 || !strings.HasPrefix(result.Prompt.PromptRevision, "sha256:") {
		t.Fatalf("empty prompt = %#v", result.Prompt)
	}
}

func TestFolderlessProjectPromptUsesNodeDefaultWithoutDiscoveringAgents(t *testing.T) {
	defaultRoot := t.TempDir()
	mustMkdirAll(t, filepath.Join(defaultRoot, "src"))
	mustWrite(t, filepath.Join(defaultRoot, "AGENTS.md"), "must not be discovered\n")
	deployment := testDeployment("")
	result, err := NewLoader(defaultRoot).Load(deployment, "src")
	if err != nil {
		t.Fatal(err)
	}
	if result.CWDRel != "src" || !result.Prompt.Complete || result.Prompt.Bytes != 0 || len(result.Prompt.Sources) != 0 {
		t.Fatalf("folderless prompt = %#v", result)
	}
	_, err = NewLoader(defaultRoot).Write(deployment, protocol.ProjectPromptWriteRequest{
		DeploymentID: deployment.ID, DeploymentRevision: deployment.AppliedRevision, Scope: ".", Content: "rules", Create: true,
	})
	requirePromptCode(t, err, protocol.ErrorDeploymentNotReady)
}

func TestProjectPromptRevisionChangesWithSourceContentAndScope(t *testing.T) {
	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, "src"))
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "one\n")
	loader := NewLoader()
	first, err := loader.Load(testDeployment(root), "src")
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "two\n")
	second, err := loader.Load(testDeployment(root), "src")
	if err != nil {
		t.Fatal(err)
	}
	if first.Prompt.PromptRevision == second.Prompt.PromptRevision || first.Prompt.Sources[0].SHA256 == second.Prompt.Sources[0].SHA256 {
		t.Fatalf("content change did not change prompt revision: first=%#v second=%#v", first.Prompt, second.Prompt)
	}
	mustWrite(t, filepath.Join(root, "src", "AGENTS.md"), "two\n")
	third, err := loader.Load(testDeployment(root), "src")
	if err != nil {
		t.Fatal(err)
	}
	if second.Prompt.PromptRevision == third.Prompt.PromptRevision || len(third.Prompt.Sources) != 2 {
		t.Fatalf("nested source scope did not change prompt revision: %#v", third.Prompt)
	}
}

func TestProjectPromptRejectsFileAndTargetBudgets(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "AGENTS.md"), strings.Repeat("x", protocol.MaxProjectPromptFileBytes+1))
	_, err := NewLoader().Load(testDeployment(root), ".")
	requirePromptCode(t, err, protocol.ErrorProjectPromptTooLarge)

	root = t.TempDir()
	current := root
	for i := 0; i < 5; i++ {
		mustWrite(t, filepath.Join(current, "AGENTS.md"), strings.Repeat(string(rune('a'+i)), 60<<10))
		if i < 4 {
			current = filepath.Join(current, "d"+string(rune('0'+i)))
			mustMkdirAll(t, current)
		}
	}
	rel, err := filepath.Rel(root, current)
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewLoader().Load(testDeployment(root), rel)
	requirePromptCode(t, err, protocol.ErrorProjectPromptTooLarge)
}

func TestProjectPromptRejectsInvalidUTF8AndNonRegularSource(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte{0xff, 0xfe}, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := NewLoader().Load(testDeployment(root), ".")
	requirePromptCode(t, err, protocol.ErrorProjectPromptReadFailed)

	root = t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "AGENTS.md"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err = NewLoader().Load(testDeployment(root), ".")
	requirePromptCode(t, err, protocol.ErrorProjectPromptReadFailed)
}

func TestProjectPromptDistinguishesUnreadableSourceFromMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("portable unreadable-file mode semantics are not available on Windows")
	}
	root := t.TempDir()
	path := filepath.Join(root, "AGENTS.md")
	mustWrite(t, path, "private rules\n")
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	_, err := NewLoader().Load(testDeployment(root), ".")
	requirePromptCode(t, err, protocol.ErrorProjectPromptReadFailed)
}

func TestProjectPromptRejectsScopeAndSourceSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require elevated privileges")
	}
	root := t.TempDir()
	outside := t.TempDir()
	mustMkdirAll(t, filepath.Join(outside, "cwd"))
	if err := os.Symlink(filepath.Join(outside, "cwd"), filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	_, err := NewLoader().Load(testDeployment(root), "escape")
	requirePromptCode(t, err, protocol.ErrorPromptScopeEscape)

	root = t.TempDir()
	outsideFile := filepath.Join(outside, "outside-agents.md")
	mustWrite(t, outsideFile, "outside\n")
	if err := os.Symlink(outsideFile, filepath.Join(root, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	_, err = NewLoader().Load(testDeployment(root), ".")
	requirePromptCode(t, err, protocol.ErrorPromptScopeEscape)
}

func TestProjectPromptRejectsSourceChangedDuringRead(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "AGENTS.md")
	mustWrite(t, path, "version-zero\n")
	loader := NewLoader()
	loader.afterRead = func(_ string, attempt int) {
		mustWrite(t, path, "version-"+strings.Repeat("x", attempt+3)+"\n")
	}
	_, err := loader.Load(testDeployment(root), ".")
	requirePromptCode(t, err, protocol.ErrorProjectPromptReadFailed)
}

func TestProjectPromptNormalizesInProjectDirectorySymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require elevated privileges")
	}
	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, "real", "auth"))
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "root\n")
	mustWrite(t, filepath.Join(root, "real", "AGENTS.md"), "real\n")
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	result, err := NewLoader().Load(testDeployment(root), "alias/auth")
	if err != nil {
		t.Fatal(err)
	}
	if result.CWDRel != "real/auth" || len(result.Prompt.Sources) != 2 || result.Prompt.Sources[1].Path != "real/AGENTS.md" {
		t.Fatalf("symlink-normalized prompt = %#v", result)
	}
}

func TestProjectPromptWriteCreatesUpdatesAndRejectsStaleCAS(t *testing.T) {
	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, "backend"))
	loader := NewLoader()
	deployment := testDeployment(root)

	created, err := loader.Write(deployment, protocol.ProjectPromptWriteRequest{
		DeploymentID: deployment.ID, DeploymentRevision: deployment.AppliedRevision,
		Scope: "backend", Content: "backend rules\n", Create: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !created.Created || created.Scope != "backend" || created.Source.Path != "backend/AGENTS.md" || created.Source.Content != "backend rules\n" || !created.Prompt.Complete || len(created.Prompt.Sources) != 1 {
		t.Fatalf("created Prompt source = %#v", created)
	}
	firstHash := created.Source.SHA256
	updated, err := loader.Write(deployment, protocol.ProjectPromptWriteRequest{
		DeploymentID: deployment.ID, DeploymentRevision: deployment.AppliedRevision,
		Scope: "backend", Content: "updated rules\n", ExpectedSHA256: firstHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Created || updated.Source.SHA256 == firstHash || updated.Source.Content != "updated rules\n" || updated.Prompt.PromptRevision == created.Prompt.PromptRevision {
		t.Fatalf("updated Prompt source = %#v", updated)
	}
	data, err := os.ReadFile(filepath.Join(root, "backend", "AGENTS.md"))
	if err != nil || string(data) != "updated rules\n" {
		t.Fatalf("written AGENTS.md = %q err=%v", data, err)
	}

	_, err = loader.Write(deployment, protocol.ProjectPromptWriteRequest{
		DeploymentID: deployment.ID, DeploymentRevision: deployment.AppliedRevision,
		Scope: "backend", Content: "stale write\n", ExpectedSHA256: firstHash,
	})
	requirePromptCode(t, err, protocol.ErrorRevisionConflict)
	_, err = loader.Write(deployment, protocol.ProjectPromptWriteRequest{
		DeploymentID: deployment.ID, DeploymentRevision: deployment.AppliedRevision,
		Scope: "backend", Content: "duplicate create\n", Create: true,
	})
	requirePromptCode(t, err, protocol.ErrorRevisionConflict)
}

func TestProjectPromptWriteRejectsEscapeAndOversize(t *testing.T) {
	root := t.TempDir()
	loader := NewLoader()
	deployment := testDeployment(root)

	_, err := loader.Write(deployment, protocol.ProjectPromptWriteRequest{
		DeploymentID: deployment.ID, DeploymentRevision: deployment.AppliedRevision,
		Scope: "../outside", Content: "rules\n", Create: true,
	})
	requirePromptCode(t, err, protocol.ErrorPromptScopeEscape)

	_, err = loader.Write(deployment, protocol.ProjectPromptWriteRequest{
		DeploymentID: deployment.ID, DeploymentRevision: deployment.AppliedRevision,
		Scope: ".", Content: strings.Repeat("x", protocol.MaxProjectPromptFileBytes+1), Create: true,
	})
	requirePromptCode(t, err, protocol.ErrorProjectPromptTooLarge)
	if _, statErr := os.Stat(filepath.Join(root, "AGENTS.md")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("rejected write created AGENTS.md: %v", statErr)
	}
}

func testDeployment(root string) protocol.Deployment {
	return protocol.Deployment{ID: "deployment-1", ProjectID: "project-1", NodeID: "node-1", WorkingFolder: root, AppliedRevision: "rev-1"}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func requirePromptCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %s", code)
	}
	var remote *protocol.RemoteError
	if !errors.As(err, &remote) {
		t.Fatalf("error %T %v is not RemoteError", err, err)
	}
	if remote.Code != code {
		t.Fatalf("error code = %q, want %q (%v)", remote.Code, code, err)
	}
}
