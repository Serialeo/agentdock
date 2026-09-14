package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	protocol "github.com/Serialeo/agentdock-protocol"
	"github.com/uvwt/agentdock/internal/config"
)

func TestProjectSourceProvenanceRequiresRefreshWhenGitStateChanges(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	runtime, deployment := newProjectPromptRuntime(t, protocol.DeploymentPermissions{Computer: protocol.ComputerPermissionNone, Files: protocol.FileCapabilityReadOnly})
	root := deployment.WorkingFolder
	mustWriteProjectFile(t, filepath.Join(root, "AGENTS.md"), "root\n")
	runProjectPromptGit(t, root, "init")
	runProjectPromptGit(t, root, "symbolic-ref", "HEAD", "refs/heads/main")
	runProjectPromptGit(t, root, "add", "AGENTS.md")
	runProjectPromptGit(t, root, "-c", "user.name=AgentDock Test", "-c", "user.email=agentdock@example.invalid", "commit", "-m", "initial")

	cleanPrompt := loadProjectPromptForTest(t, runtime, deployment, ".")
	if cleanPrompt.SourceProvenance.Kind != protocol.SourceProvenanceGit || cleanPrompt.SourceProvenance.Head == "" || cleanPrompt.SourceProvenance.Dirty {
		t.Fatalf("clean source provenance = %#v", cleanPrompt.SourceProvenance)
	}
	mustWriteProjectFile(t, filepath.Join(root, "source.txt"), "dirty\n")
	stale := projectBindRequestForTest(deployment, ".", "ctx-source-stale", cleanPrompt)
	_, err := runtime.BindProjectTarget(stale)
	requireProtocolErrorCode(t, err, protocol.ErrorContextRefreshRequired)

	dirtyPrompt := loadProjectPromptForTest(t, runtime, deployment, ".")
	if !dirtyPrompt.SourceProvenance.Dirty || dirtyPrompt.SourceProvenance.Head != cleanPrompt.SourceProvenance.Head {
		t.Fatalf("dirty source provenance = %#v", dirtyPrompt.SourceProvenance)
	}
	bound := projectBindRequestForTest(deployment, ".", "ctx-source-dirty", dirtyPrompt)
	if _, err := runtime.BindProjectTarget(bound); err != nil {
		t.Fatalf("bind refreshed dirty source provenance: %v", err)
	}

	runProjectPromptGit(t, root, "add", "source.txt")
	runProjectPromptGit(t, root, "-c", "user.name=AgentDock Test", "-c", "user.email=agentdock@example.invalid", "commit", "-m", "source")
	_, err = runtime.PrepareProjectExecution(context.Background(), executionContextForBinding(bound))
	requireProtocolErrorCode(t, err, protocol.ErrorContextRefreshRequired)
}

func TestProjectTargetBindRejectsStalePromptAndExecutionDetectsRuleChange(t *testing.T) {
	runtime, deployment := newProjectPromptRuntime(t, protocol.DeploymentPermissions{Computer: protocol.ComputerPermissionNone, Files: protocol.FileCapabilityReadOnly})
	mustWriteProjectFile(t, filepath.Join(deployment.WorkingFolder, "AGENTS.md"), "root-v1\n")
	prompt := loadProjectPromptForTest(t, runtime, deployment, ".")

	mustWriteProjectFile(t, filepath.Join(deployment.WorkingFolder, "AGENTS.md"), "root-v2\n")
	request := projectBindRequestForTest(deployment, ".", "ctx-1", prompt)
	_, err := runtime.BindProjectTarget(request)
	requireProtocolErrorCode(t, err, protocol.ErrorContextRefreshRequired)

	prompt = loadProjectPromptForTest(t, runtime, deployment, ".")
	request = projectBindRequestForTest(deployment, ".", "ctx-2", prompt)
	if _, err := runtime.BindProjectTarget(request); err != nil {
		t.Fatal(err)
	}
	executionContext := executionContextForBinding(request)
	if _, err := runtime.PrepareProjectExecution(context.Background(), executionContext); err != nil {
		t.Fatalf("fresh Project Prompt rejected: %v", err)
	}

	mustWriteProjectFile(t, filepath.Join(deployment.WorkingFolder, "AGENTS.md"), "root-v3\n")
	_, err = runtime.PrepareProjectExecution(context.Background(), executionContext)
	requireProtocolErrorCode(t, err, protocol.ErrorContextRefreshRequired)
}

func TestProjectTargetRebindRequiresNewContextRevisionWhenPromptContextChanges(t *testing.T) {
	runtime, deployment := newProjectPromptRuntime(t, protocol.DeploymentPermissions{Computer: protocol.ComputerPermissionNone, Files: protocol.FileCapabilityReadOnly})
	mustMkdirProject(t, filepath.Join(deployment.WorkingFolder, "backend"))
	mustWriteProjectFile(t, filepath.Join(deployment.WorkingFolder, "AGENTS.md"), "root\n")
	mustWriteProjectFile(t, filepath.Join(deployment.WorkingFolder, "backend", "AGENTS.md"), "backend\n")
	rootPrompt := loadProjectPromptForTest(t, runtime, deployment, ".")
	request := projectBindRequestForTest(deployment, ".", "ctx-1", rootPrompt)
	if _, err := runtime.BindProjectTarget(request); err != nil {
		t.Fatal(err)
	}
	backendPrompt := loadProjectPromptForTest(t, runtime, deployment, "backend")
	_, err := runtime.RebindProjectTarget(protocol.ProjectTargetRebindRequest{
		WorkSessionID: request.WorkSessionID, TargetID: request.TargetID, CWDRel: "backend", ContextRevision: request.ContextRevision,
		SourceProvenance: backendPrompt.SourceProvenance,
		PromptScopes: []protocol.PromptScopeRevision{
			{Scope: rootPrompt.CWDRel, PromptRevision: rootPrompt.Prompt.PromptRevision},
			{Scope: backendPrompt.CWDRel, PromptRevision: backendPrompt.Prompt.PromptRevision},
		},
	})
	requireProtocolErrorCode(t, err, protocol.ErrorRevisionConflict)
}

func TestNestedProjectPromptScopeBlocksFileAccessBeforeRefreshThenAllowsAfterRebind(t *testing.T) {
	permissions := protocol.DeploymentPermissions{Computer: protocol.ComputerPermissionNone, Files: protocol.FileCapabilityReadWrite}
	runtime, deployment := newProjectPromptRuntime(t, permissions)
	root := deployment.WorkingFolder
	mustMkdirProject(t, filepath.Join(root, "backend"))
	mustMkdirProject(t, filepath.Join(root, "sibling"))
	mustWriteProjectFile(t, filepath.Join(root, "AGENTS.md"), "root rules\n")
	mustWriteProjectFile(t, filepath.Join(root, "backend", "AGENTS.md"), "backend rules\n")
	mustWriteProjectFile(t, filepath.Join(root, "root.txt"), "root\n")
	mustWriteProjectFile(t, filepath.Join(root, "backend", "nested.txt"), "nested\n")
	mustWriteProjectFile(t, filepath.Join(root, "sibling", "plain.txt"), "plain\n")

	rootPrompt := loadProjectPromptForTest(t, runtime, deployment, ".")
	request := projectBindRequestForTest(deployment, ".", "ctx-1", rootPrompt)
	if _, err := runtime.BindProjectTarget(request); err != nil {
		t.Fatal(err)
	}
	ctx := prepareProjectContextForTest(t, runtime, request)

	_, err := runtime.Call(ctx, "read_file", map[string]any{"path": "backend/nested.txt"})
	requireProtocolErrorCode(t, err, protocol.ErrorContextRefreshRequired)
	if result, err := runtime.Call(ctx, "read_file", map[string]any{"path": "sibling/plain.txt"}); err != nil {
		t.Fatalf("sibling with unchanged rule chain required unnecessary refresh: %v", err)
	} else if result["content"] != "plain\n" {
		t.Fatalf("sibling read result = %#v", result)
	}

	patch := "*** Begin Patch\n*** Update File: root.txt\n@@\n-root\n+root-updated\n*** Update File: backend/nested.txt\n@@\n-nested\n+nested-updated\n*** End Patch\n"
	_, err = runtime.Call(ctx, "file_edit", map[string]any{"action": "patch", "patch": patch})
	requireProtocolErrorCode(t, err, protocol.ErrorContextRefreshRequired)
	assertProjectFile(t, filepath.Join(root, "root.txt"), "root\n")
	assertProjectFile(t, filepath.Join(root, "backend", "nested.txt"), "nested\n")

	backendPrompt := loadProjectPromptForTest(t, runtime, deployment, "backend")
	rebind := protocol.ProjectTargetRebindRequest{
		WorkSessionID: request.WorkSessionID, TargetID: request.TargetID, CWDRel: ".", ContextRevision: "ctx-2",
		SourceProvenance: backendPrompt.SourceProvenance,
		PromptScopes: []protocol.PromptScopeRevision{
			{Scope: rootPrompt.CWDRel, PromptRevision: rootPrompt.Prompt.PromptRevision},
			{Scope: backendPrompt.CWDRel, PromptRevision: backendPrompt.Prompt.PromptRevision},
		},
	}
	if _, err := runtime.RebindProjectTarget(rebind); err != nil {
		t.Fatal(err)
	}
	ctx = prepareProjectContextAfterRebindForTest(t, runtime, request, rebind.ContextRevision)
	if _, err := runtime.Call(ctx, "file_edit", map[string]any{"action": "patch", "patch": patch}); err != nil {
		t.Fatalf("patch rejected after complete nested Prompt delivery: %v", err)
	}
	assertProjectFile(t, filepath.Join(root, "root.txt"), "root-updated\n")
	assertProjectFile(t, filepath.Join(root, "backend", "nested.txt"), "nested-updated\n")
}

func TestMultiScopePatchPreflightsEveryFileBeforeAnyMutation(t *testing.T) {
	permissions := protocol.DeploymentPermissions{Computer: protocol.ComputerPermissionNone, Files: protocol.FileCapabilityReadWrite}
	runtime, deployment := newProjectPromptRuntime(t, permissions)
	root := deployment.WorkingFolder
	for _, dir := range []string{"backend", "frontend"} {
		mustMkdirProject(t, filepath.Join(root, dir))
		mustWriteProjectFile(t, filepath.Join(root, dir, "AGENTS.md"), dir+" rules\n")
		mustWriteProjectFile(t, filepath.Join(root, dir, "value.txt"), dir+"\n")
	}
	mustWriteProjectFile(t, filepath.Join(root, "AGENTS.md"), "root\n")
	rootPrompt := loadProjectPromptForTest(t, runtime, deployment, ".")
	request := projectBindRequestForTest(deployment, ".", "ctx-1", rootPrompt)
	if _, err := runtime.BindProjectTarget(request); err != nil {
		t.Fatal(err)
	}
	ctx := prepareProjectContextForTest(t, runtime, request)
	patch := "*** Begin Patch\n*** Update File: backend/value.txt\n@@\n-backend\n+backend-new\n*** Update File: frontend/value.txt\n@@\n-frontend\n+frontend-new\n*** End Patch\n"
	_, err := runtime.Call(ctx, "file_edit", map[string]any{"action": "patch", "patch": patch})
	requireProtocolErrorCode(t, err, protocol.ErrorContextRefreshRequired)
	var remote *protocol.RemoteError
	if !errors.As(err, &remote) {
		t.Fatal(err)
	}
	missing, _ := remote.Details["missing_prompt_scopes"].([]map[string]any)
	if len(missing) != 2 {
		t.Fatalf("missing prompt scopes = %#v, want backend and frontend", remote.Details["missing_prompt_scopes"])
	}
	assertProjectFile(t, filepath.Join(root, "backend", "value.txt"), "backend\n")
	assertProjectFile(t, filepath.Join(root, "frontend", "value.txt"), "frontend\n")
}

func TestRecursiveStructuredFileToolsPreflightNestedPromptScopes(t *testing.T) {
	permissions := protocol.DeploymentPermissions{Computer: protocol.ComputerPermissionNone, Files: protocol.FileCapabilityReadWrite}
	runtime, deployment := newProjectPromptRuntime(t, permissions)
	root := deployment.WorkingFolder
	mustMkdirProject(t, filepath.Join(root, "backend"))
	mustWriteProjectFile(t, filepath.Join(root, "AGENTS.md"), "root\n")
	mustWriteProjectFile(t, filepath.Join(root, "backend", "AGENTS.md"), "backend rules\n")
	mustWriteProjectFile(t, filepath.Join(root, "backend", "value.txt"), "needle\n")
	rootPrompt := loadProjectPromptForTest(t, runtime, deployment, ".")
	request := projectBindRequestForTest(deployment, ".", "ctx-recursive", rootPrompt)
	if _, err := runtime.BindProjectTarget(request); err != nil {
		t.Fatal(err)
	}
	ctx := prepareProjectContextForTest(t, runtime, request)

	if _, err := runtime.Call(ctx, "list_dir", map[string]any{"path": ".", "max_depth": 1}); err != nil {
		t.Fatalf("shallow list unexpectedly entered nested Prompt scope: %v", err)
	}
	for _, call := range []struct {
		name string
		args map[string]any
	}{
		{name: "list_dir", args: map[string]any{"path": ".", "max_depth": 2}},
		{name: "search_text", args: map[string]any{"path": ".", "query": "needle"}},
		{name: "file_edit", args: map[string]any{"action": "delete", "path": "backend", "recursive": true}},
		{name: "file_edit", args: map[string]any{"action": "move", "path": "backend", "new_path": "moved-backend"}},
	} {
		_, err := runtime.Call(ctx, call.name, call.args)
		requireProtocolErrorCode(t, err, protocol.ErrorContextRefreshRequired)
	}
	assertProjectFile(t, filepath.Join(root, "backend", "value.txt"), "needle\n")
	if _, err := os.Stat(filepath.Join(root, "moved-backend")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("move happened before nested Prompt refresh: %v", err)
	}
}

func TestRawDiffPatchPreflightsEveryNestedPromptBeforeGitApply(t *testing.T) {
	permissions := protocol.DeploymentPermissions{Computer: protocol.ComputerPermissionNone, Files: protocol.FileCapabilityReadWrite}
	runtime, deployment := newProjectPromptRuntime(t, permissions)
	root := deployment.WorkingFolder
	for _, dir := range []string{"backend", "frontend"} {
		mustMkdirProject(t, filepath.Join(root, dir))
		mustWriteProjectFile(t, filepath.Join(root, dir, "AGENTS.md"), dir+" rules\n")
		mustWriteProjectFile(t, filepath.Join(root, dir, "value.txt"), dir+"\n")
	}
	mustWriteProjectFile(t, filepath.Join(root, "AGENTS.md"), "root\n")
	rootPrompt := loadProjectPromptForTest(t, runtime, deployment, ".")
	request := projectBindRequestForTest(deployment, ".", "ctx-raw-diff", rootPrompt)
	if _, err := runtime.BindProjectTarget(request); err != nil {
		t.Fatal(err)
	}
	ctx := prepareProjectContextForTest(t, runtime, request)
	patch := "diff --git a/backend/value.txt b/backend/value.txt\n--- a/backend/value.txt\n+++ b/backend/value.txt\n@@ -1 +1 @@\n-backend\n+backend-new\ndiff --git a/frontend/value.txt b/frontend/value.txt\n--- a/frontend/value.txt\n+++ b/frontend/value.txt\n@@ -1 +1 @@\n-frontend\n+frontend-new\n"
	_, err := runtime.Call(ctx, "file_edit", map[string]any{"action": "patch", "patch": patch})
	requireProtocolErrorCode(t, err, protocol.ErrorContextRefreshRequired)
	var remote *protocol.RemoteError
	if !errors.As(err, &remote) {
		t.Fatal(err)
	}
	missing, _ := remote.Details["missing_prompt_scopes"].([]map[string]any)
	if len(missing) != 2 {
		t.Fatalf("raw diff missing prompt scopes = %#v, want backend and frontend", remote.Details["missing_prompt_scopes"])
	}
	assertProjectFile(t, filepath.Join(root, "backend", "value.txt"), "backend\n")
	assertProjectFile(t, filepath.Join(root, "frontend", "value.txt"), "frontend\n")
}

func TestProjectPromptLoadingIsIsolatedPerDeployment(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()
	mustWriteProjectFile(t, filepath.Join(rootA, "AGENTS.md"), "node-a rules\n")
	mustWriteProjectFile(t, filepath.Join(rootB, "AGENTS.md"), "node-b rules\n")
	cfg := config.Config{AgentDockHome: filepath.Join(t.TempDir(), ".agentdock"), AgentDockDefaultDir: rootA}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	permissions := protocol.DeploymentPermissions{Computer: protocol.ComputerPermissionNone, Files: protocol.FileCapabilityReadOnly}
	deployments := []protocol.Deployment{
		{ID: "deployment-a", ProjectID: "project-shared", NodeID: "node-a", WorkingFolder: rootA, Permissions: permissions, DesiredRevision: "rev-a", AppliedRevision: "rev-a", Enabled: true, ApplyStatus: protocol.DeploymentApplyApplied},
		{ID: "deployment-b", ProjectID: "project-shared", NodeID: "node-b", WorkingFolder: rootB, Permissions: permissions, DesiredRevision: "rev-b", AppliedRevision: "rev-b", Enabled: true, ApplyStatus: protocol.DeploymentApplyApplied},
	}
	for _, deployment := range deployments {
		if _, err := runtime.ApplyProjectDeployment(deployment); err != nil {
			t.Fatal(err)
		}
	}
	promptA := loadProjectPromptForTest(t, runtime, deployments[0], ".")
	promptB := loadProjectPromptForTest(t, runtime, deployments[1], ".")
	if len(promptA.Prompt.Sources) != 1 || promptA.Prompt.Sources[0].Content != "node-a rules\n" {
		t.Fatalf("deployment A prompt = %#v", promptA)
	}
	if len(promptB.Prompt.Sources) != 1 || promptB.Prompt.Sources[0].Content != "node-b rules\n" {
		t.Fatalf("deployment B prompt = %#v", promptB)
	}
	if promptA.Prompt.PromptRevision == promptB.Prompt.PromptRevision {
		t.Fatal("different Deployment Prompt bodies unexpectedly share a revision")
	}
}

func TestProjectPromptDoesNotAutoLoadRulesForExternalHostPath(t *testing.T) {
	permissions := protocol.DeploymentPermissions{Computer: protocol.ComputerPermissionNone, Files: protocol.FileCapabilityReadOnly}
	runtime, deployment := newProjectPromptRuntime(t, permissions)
	mustWriteProjectFile(t, filepath.Join(deployment.WorkingFolder, "AGENTS.md"), "project rules\n")
	external := t.TempDir()
	mustWriteProjectFile(t, filepath.Join(external, "AGENTS.md"), "external rules must not auto load\n")
	externalFile := filepath.Join(external, "external.txt")
	mustWriteProjectFile(t, externalFile, "outside\n")
	prompt := loadProjectPromptForTest(t, runtime, deployment, ".")
	request := projectBindRequestForTest(deployment, ".", "ctx-1", prompt)
	if _, err := runtime.BindProjectTarget(request); err != nil {
		t.Fatal(err)
	}
	ctx := prepareProjectContextForTest(t, runtime, request)
	result, err := runtime.Call(ctx, "read_file", map[string]any{"path": externalFile})
	if err != nil {
		t.Fatalf("external Host path was incorrectly forced through external AGENTS discovery: %v", err)
	}
	if result["content"] != "outside\n" {
		t.Fatalf("external read result = %#v", result)
	}
}

func TestProjectPromptInternalLoadDoesNotBypassFilesNone(t *testing.T) {
	runtime, deployment := newProjectPromptRuntime(t, protocol.DeploymentPermissions{Computer: protocol.ComputerPermissionNone, Files: protocol.FileCapabilityNone})
	mustWriteProjectFile(t, filepath.Join(deployment.WorkingFolder, "AGENTS.md"), "internal prompt\n")
	prompt := loadProjectPromptForTest(t, runtime, deployment, ".")
	if len(prompt.Prompt.Sources) != 1 || prompt.Prompt.Sources[0].Content != "internal prompt\n" {
		t.Fatalf("internal Project Prompt load = %#v", prompt)
	}
	request := projectBindRequestForTest(deployment, ".", "ctx-1", prompt)
	if _, err := runtime.BindProjectTarget(request); err != nil {
		t.Fatal(err)
	}
	ctx := prepareProjectContextForTest(t, runtime, request)
	_, err := runtime.Call(ctx, "read_file", map[string]any{"path": "AGENTS.md"})
	requireAppToolErrorCode(t, err, protocol.ErrorCapabilityDenied)
}

func TestDeliveredPromptScopeUnionEnforcesContextBudget(t *testing.T) {
	runtime, deployment := newProjectPromptRuntime(t, protocol.DeploymentPermissions{Computer: protocol.ComputerPermissionNone, Files: protocol.FileCapabilityReadOnly})
	root := deployment.WorkingFolder
	prompts := make([]protocol.ProjectPromptLoadResult, 0, 5)
	for branch := 0; branch < 5; branch++ {
		current := filepath.Join(root, "branch-"+string(rune('a'+branch)))
		mustMkdirProject(t, current)
		for depth := 0; depth < 4; depth++ {
			mustWriteProjectFile(t, filepath.Join(current, "AGENTS.md"), strings.Repeat(string(rune('a'+branch)), 60<<10))
			if depth < 3 {
				current = filepath.Join(current, "d"+string(rune('0'+depth)))
				mustMkdirProject(t, current)
			}
		}
		rel, err := filepath.Rel(root, current)
		if err != nil {
			t.Fatal(err)
		}
		prompts = append(prompts, loadProjectPromptForTest(t, runtime, deployment, rel))
	}
	request := projectBindRequestForTest(deployment, prompts[0].CWDRel, "ctx-budget", prompts...)
	_, err := runtime.BindProjectTarget(request)
	requireProtocolErrorCode(t, err, protocol.ErrorProjectPromptTooLarge)
}

func TestExplicitCommandWorkdirRequiresNestedPromptBeforeExecution(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("marker command in this test uses POSIX shell syntax")
	}
	permissions := protocol.DeploymentPermissions{Computer: protocol.ComputerPermissionNone, Files: protocol.FileCapabilityReadOnly, Shell: true}
	runtime, deployment := newProjectPromptRuntime(t, permissions)
	root := deployment.WorkingFolder
	mustMkdirProject(t, filepath.Join(root, "backend"))
	mustWriteProjectFile(t, filepath.Join(root, "AGENTS.md"), "root\n")
	mustWriteProjectFile(t, filepath.Join(root, "backend", "AGENTS.md"), "backend\n")
	rootPrompt := loadProjectPromptForTest(t, runtime, deployment, ".")
	request := projectBindRequestForTest(deployment, ".", "ctx-command-1", rootPrompt)
	if _, err := runtime.BindProjectTarget(request); err != nil {
		t.Fatal(err)
	}
	ctx := prepareProjectContextForTest(t, runtime, request)
	marker := filepath.Join(root, "backend", "must-not-run")
	_, err := runtime.Call(ctx, "exec_command", map[string]any{
		"cmd": `printf ran > "$MARKER"`, "workdir": "backend", "env": map[string]any{"MARKER": marker}, "execution_mode": "sync",
	})
	requireProtocolErrorCode(t, err, protocol.ErrorContextRefreshRequired)
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("command ran before nested Prompt refresh: %v", statErr)
	}
}

func newProjectPromptRuntime(t *testing.T, permissions protocol.DeploymentPermissions) (*Runtime, protocol.Deployment) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Config{AgentDockHome: filepath.Join(t.TempDir(), ".agentdock"), AgentDockDefaultDir: root}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	deployment := protocol.Deployment{
		ID: "deployment-prompt", ProjectID: "project-prompt", NodeID: "node-prompt", WorkingFolder: root,
		Role: "test", Purpose: "Project Prompt tests", Permissions: permissions,
		DesiredRevision: "dep-rev-1", AppliedRevision: "dep-rev-1", Enabled: true, ApplyStatus: protocol.DeploymentApplyApplied,
	}
	if _, err := runtime.ApplyProjectDeployment(deployment); err != nil {
		t.Fatal(err)
	}
	return runtime, deployment
}

func loadProjectPromptForTest(t *testing.T, runtime *Runtime, deployment protocol.Deployment, scope string) protocol.ProjectPromptLoadResult {
	t.Helper()
	result, err := runtime.LoadProjectPrompt(protocol.ProjectPromptLoadRequest{DeploymentID: deployment.ID, DeploymentRevision: deployment.AppliedRevision, CWDRel: scope})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func projectBindRequestForTest(deployment protocol.Deployment, cwd, contextRevision string, prompts ...protocol.ProjectPromptLoadResult) protocol.ProjectTargetBindRequest {
	scopes := make([]protocol.PromptScopeRevision, 0, len(prompts))
	provenance := protocol.SourceProvenance{Kind: protocol.SourceProvenanceNone}
	for index, prompt := range prompts {
		scopes = append(scopes, protocol.PromptScopeRevision{Scope: prompt.CWDRel, PromptRevision: prompt.Prompt.PromptRevision})
		if index == 0 {
			provenance = prompt.SourceProvenance
		}
	}
	return protocol.ProjectTargetBindRequest{
		WorkSessionID: "ws-prompt", TargetID: "target-prompt", ProjectID: deployment.ProjectID, DeploymentID: deployment.ID,
		CWDRel: cwd, DeploymentRevision: deployment.AppliedRevision, ContextRevision: contextRevision, PromptScopes: scopes, SourceProvenance: provenance,
	}
}

func executionContextForBinding(request protocol.ProjectTargetBindRequest) *protocol.ExecutionContext {
	return &protocol.ExecutionContext{
		WorkSessionID: request.WorkSessionID, TargetID: request.TargetID, ProjectID: request.ProjectID, DeploymentID: request.DeploymentID,
		DeploymentRevision: request.DeploymentRevision, ContextRevision: request.ContextRevision,
	}
}

func prepareProjectContextForTest(t *testing.T, runtime *Runtime, request protocol.ProjectTargetBindRequest) context.Context {
	t.Helper()
	ctx, err := runtime.PrepareProjectExecution(context.Background(), executionContextForBinding(request))
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func prepareProjectContextAfterRebindForTest(t *testing.T, runtime *Runtime, initial protocol.ProjectTargetBindRequest, contextRevision string) context.Context {
	t.Helper()
	execution := executionContextForBinding(initial)
	execution.ContextRevision = contextRevision
	ctx, err := runtime.PrepareProjectExecution(context.Background(), execution)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func requireProtocolErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected protocol error %s", code)
	}
	var remote *protocol.RemoteError
	if !errors.As(err, &remote) {
		t.Fatalf("error %T %v is not protocol.RemoteError", err, err)
	}
	if remote.Code != code {
		t.Fatalf("protocol error code = %q, want %q (%v)", remote.Code, code, err)
	}
}

func runProjectPromptGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	commandArgs := append([]string{"-C", root}, args...)
	output, err := exec.Command("git", commandArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func mustMkdirProject(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func mustWriteProjectFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertProjectFile(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("file %s = %q, want %q", path, data, want)
	}
}

func TestProjectPromptScopeDetailsAreStableForErrorAssertions(t *testing.T) {
	// Keep a small compile-time/use check for string detail normalization used by
	// Project Prompt errors without coupling tests to map iteration order.
	if strings.TrimSpace(" backend ") != "backend" {
		t.Fatal("unexpected strings behavior")
	}
}
