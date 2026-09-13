package project

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	protocol "github.com/Serialeo/agentdock-protocol"
	"github.com/uvwt/agentdock/internal/config"
)

func TestAppliedDeploymentsPersistButTargetBindingsDoNot(t *testing.T) {
	home := t.TempDir()
	working := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(working, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := New(config.Config{AgentDockHome: home})
	if err != nil {
		t.Fatal(err)
	}
	deployment := testDeployment(working, "rev-1", protocol.FileCapabilityReadOnly)
	if _, err := store.ApplyDeployment(deployment); err != nil {
		t.Fatal(err)
	}
	binding, err := store.BindTarget(testBinding("src", "rev-1", "ctx-1"))
	if err != nil {
		t.Fatal(err)
	}
	if binding.CWDRel != "src" || binding.CWDAbs != filepath.Join(working, "src") {
		t.Fatalf("binding = %#v", binding)
	}

	reloaded, err := New(config.Config{AgentDockHome: home})
	if err != nil {
		t.Fatal(err)
	}
	persisted, ok := reloaded.Deployment(deployment.ID)
	if !ok || persisted.AppliedRevision != "rev-1" || persisted.Permissions.Files != protocol.FileCapabilityReadOnly {
		t.Fatalf("persisted deployment = %#v, %v", persisted, ok)
	}
	_, err = reloaded.ResolveExecution(testExecutionContext("rev-1", "ctx-1"))
	requireRemoteCode(t, err, protocol.ErrorSessionTargetDenied)
}

func TestResolveExecutionUsesAppliedDeploymentPermissionsAndBoundTarget(t *testing.T) {
	working := t.TempDir()
	if err := os.Mkdir(filepath.Join(working, "backend"), 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := New(config.Config{AgentDockHome: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	deployment := testDeployment(working, "rev-7", protocol.FileCapabilityReadWrite)
	deployment.Permissions.Shell = true
	if _, err := store.ApplyDeployment(deployment); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BindTarget(testBinding("backend", "rev-7", "ctx-7")); err != nil {
		t.Fatal(err)
	}
	execution, err := store.ResolveExecution(testExecutionContext("rev-7", "ctx-7"))
	if err != nil {
		t.Fatal(err)
	}
	if execution.CWD != filepath.Join(working, "backend") {
		t.Fatalf("execution cwd = %q", execution.CWD)
	}
	if execution.Permissions.Files != protocol.FileCapabilityReadWrite || !execution.Permissions.Shell {
		t.Fatalf("trusted permissions = %#v", execution.Permissions)
	}

	spoofed := testExecutionContext("rev-7", "ctx-7")
	spoofed.ProjectID = "other-project"
	_, err = store.ResolveExecution(spoofed)
	requireRemoteCode(t, err, protocol.ErrorSessionTargetDenied)
}

func TestFolderlessDeploymentUsesNodeDefaultCWDAndKeepsFullAccess(t *testing.T) {
	defaultCWD := t.TempDir()
	if err := os.Mkdir(filepath.Join(defaultCWD, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := New(config.Config{AgentDockHome: t.TempDir(), AgentDockDefaultDir: defaultCWD})
	if err != nil {
		t.Fatal(err)
	}
	deployment := testDeployment("", "rev-1", protocol.FileCapabilityNone)
	deployment.Permissions.FullAccess = true
	if _, err := store.ApplyDeployment(deployment); err != nil {
		t.Fatal(err)
	}
	binding := testBinding("src", "rev-1", "ctx-folderless")
	binding.SourceProvenance = protocol.SourceProvenance{Kind: protocol.SourceProvenanceNone}
	if _, err := store.BindTarget(binding); err != nil {
		t.Fatal(err)
	}
	execution, err := store.ResolveExecution(&protocol.ExecutionContext{
		WorkSessionID: "ws-1", TargetID: "target-1", ProjectID: "project-1", DeploymentID: "deployment-1",
		DeploymentRevision: "rev-1", ContextRevision: "ctx-folderless",
	})
	if err != nil {
		t.Fatal(err)
	}
	if execution.CWD != filepath.Join(defaultCWD, "src") {
		t.Fatalf("folderless execution cwd = %q, want %q", execution.CWD, filepath.Join(defaultCWD, "src"))
	}
	if !execution.Permissions.FullAccess {
		t.Fatalf("folderless Full Access lost: %#v", execution.Permissions)
	}
}

func TestDeploymentRevisionChangeBlocksNewExecutionButPreservesSessionControl(t *testing.T) {
	working := t.TempDir()
	store, err := New(config.Config{AgentDockHome: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyDeployment(testDeployment(working, "rev-1", protocol.FileCapabilityReadOnly)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BindTarget(testBinding(".", "rev-1", "ctx-1")); err != nil {
		t.Fatal(err)
	}

	updated := testDeployment(working, "rev-2", protocol.FileCapabilityNone)
	if _, err := store.ApplyDeployment(updated); err != nil {
		t.Fatal(err)
	}
	_, err = store.ResolveExecution(testExecutionContext("rev-1", "ctx-1"))
	requireRemoteCode(t, err, protocol.ErrorRevisionConflict)

	control, err := store.ResolveSessionControlExecution(testExecutionContext("rev-1", "ctx-1"))
	if err != nil {
		t.Fatalf("historical owner lost session control after permission change: %v", err)
	}
	if control.Target.Revoked || control.Permissions.Files != protocol.FileCapabilityNone {
		t.Fatalf("session-control execution = %#v", control)
	}

	_, err = store.BindTarget(BindTargetRequest{
		WorkSessionID: "ws-2", TargetID: "target-2", ProjectID: "project-1", DeploymentID: "deployment-1",
		CWDRel: ".", DeploymentRevision: "rev-1", ContextRevision: "ctx-2",
		PromptScopes:     []protocol.PromptScopeRevision{{Scope: ".", PromptRevision: "prompt-rev-1"}},
		SourceProvenance: protocol.SourceProvenance{Kind: protocol.SourceProvenanceNone},
	})
	requireRemoteCode(t, err, protocol.ErrorRevisionConflict)
}

func TestExplicitTargetRevokeBlocksNewExecutionButPreservesSessionControl(t *testing.T) {
	working := t.TempDir()
	store, err := New(config.Config{AgentDockHome: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyDeployment(testDeployment(working, "rev-1", protocol.FileCapabilityReadOnly)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BindTarget(testBinding(".", "rev-1", "ctx-1")); err != nil {
		t.Fatal(err)
	}
	store.RevokeTarget("target-1")
	_, err = store.ResolveExecution(testExecutionContext("rev-1", "ctx-1"))
	requireRemoteCode(t, err, protocol.ErrorSessionTargetDenied)
	control, err := store.ResolveSessionControlExecution(testExecutionContext("rev-1", "ctx-1"))
	if err != nil {
		t.Fatalf("revoked owner lost cleanup control: %v", err)
	}
	if !control.Target.Revoked {
		t.Fatalf("session control did not preserve revoked state: %#v", control.Target)
	}
}

func TestTargetCWDRejectsLexicalAndSymlinkEscape(t *testing.T) {
	working := t.TempDir()
	outside := t.TempDir()
	store, err := New(config.Config{AgentDockHome: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyDeployment(testDeployment(working, "rev-1", protocol.FileCapabilityReadOnly)); err != nil {
		t.Fatal(err)
	}
	request := testBinding("../outside", "rev-1", "ctx-1")
	_, err = store.BindTarget(request)
	requireRemoteCode(t, err, protocol.ErrorPromptScopeEscape)

	if runtime.GOOS == "windows" {
		return
	}
	if err := os.Symlink(outside, filepath.Join(working, "escape")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	request = testBinding("escape", "rev-1", "ctx-1")
	request.TargetID = "target-symlink"
	_, err = store.BindTarget(request)
	requireRemoteCode(t, err, protocol.ErrorPromptScopeEscape)
}

func TestTargetBindingIsIdempotentButCannotBeReusedForAnotherSession(t *testing.T) {
	working := t.TempDir()
	store, err := New(config.Config{AgentDockHome: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyDeployment(testDeployment(working, "rev-1", protocol.FileCapabilityReadOnly)); err != nil {
		t.Fatal(err)
	}
	request := testBinding(".", "rev-1", "ctx-1")
	first, err := store.BindTarget(request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.BindTarget(request)
	if err != nil || !sameBinding(second, first) || second.Revoked != first.Revoked {
		t.Fatalf("idempotent binding = %#v, %v", second, err)
	}
	request.WorkSessionID = "ws-other"
	_, err = store.BindTarget(request)
	requireRemoteCode(t, err, protocol.ErrorSessionTargetDenied)
}

func testDeployment(working, revision string, files protocol.FileCapability) protocol.Deployment {
	return protocol.Deployment{
		ID: "deployment-1", ProjectID: "project-1", NodeID: "node-1", WorkingFolder: working,
		Role: "linux", Purpose: "tests", Permissions: protocol.DeploymentPermissions{Files: files},
		DesiredRevision: revision, AppliedRevision: revision, Enabled: true, ApplyStatus: protocol.DeploymentApplyApplied,
	}
}

func testBinding(cwd, revision, contextRevision string) BindTargetRequest {
	return BindTargetRequest{
		WorkSessionID: "ws-1", TargetID: "target-1", ProjectID: "project-1", DeploymentID: "deployment-1",
		CWDRel: cwd, DeploymentRevision: revision, ContextRevision: contextRevision,
		PromptScopes:     []protocol.PromptScopeRevision{{Scope: cwd, PromptRevision: "prompt-rev-1"}},
		SourceProvenance: protocol.SourceProvenance{Kind: protocol.SourceProvenanceNone},
	}
}

func testExecutionContext(revision, contextRevision string) *protocol.ExecutionContext {
	return &protocol.ExecutionContext{
		WorkSessionID: "ws-1", TargetID: "target-1", ProjectID: "project-1", DeploymentID: "deployment-1",
		DeploymentRevision: revision, ContextRevision: contextRevision,
	}
}

func requireRemoteCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error code %s", code)
	}
	var remote *protocol.RemoteError
	if !errors.As(err, &remote) {
		t.Fatalf("error %T %v is not RemoteError", err, err)
	}
	if remote.Code != code {
		t.Fatalf("error code = %q, want %q (%v)", remote.Code, code, err)
	}
}
