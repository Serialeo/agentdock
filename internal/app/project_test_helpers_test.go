package app

import (
	"context"
	"testing"

	protocol "github.com/Serialeo/agentdock-protocol"
)

func fullProjectPermissionsForTest() protocol.DeploymentPermissions {
	return protocol.DeploymentPermissions{
		Files:      protocol.FileCapabilityReadWrite,
		Shell:      true,
		Browser:    true,
		DynamicMCP: true,
		ACP:        true,
	}
}

func projectContextForTest(t *testing.T, runtime *Runtime, workingFolder string, permissions protocol.DeploymentPermissions) context.Context {
	t.Helper()
	deployment := protocol.Deployment{
		ID:              "test-deployment",
		ProjectID:       "test-project",
		NodeID:          "test-node",
		WorkingFolder:   workingFolder,
		Role:            "test",
		Purpose:         "runtime test target",
		Permissions:     permissions,
		DesiredRevision: "test-rev-1",
		AppliedRevision: "test-rev-1",
		Enabled:         true,
		ApplyStatus:     protocol.DeploymentApplyApplied,
	}
	if _, err := runtime.ApplyProjectDeployment(deployment); err != nil {
		t.Fatalf("apply test Project Deployment: %v", err)
	}
	prompt, err := runtime.LoadProjectPrompt(protocol.ProjectPromptLoadRequest{
		DeploymentID: deployment.ID, DeploymentRevision: deployment.AppliedRevision, CWDRel: ".",
	})
	if err != nil {
		t.Fatalf("load test Project Prompt: %v", err)
	}
	binding := protocol.ProjectTargetBindRequest{
		WorkSessionID:      "test-work-session",
		TargetID:           "test-target",
		ProjectID:          deployment.ProjectID,
		DeploymentID:       deployment.ID,
		CWDRel:             ".",
		DeploymentRevision: deployment.AppliedRevision,
		ContextRevision:    "test-context-rev-1",
		PromptScopes:       []protocol.PromptScopeRevision{{Scope: prompt.CWDRel, PromptRevision: prompt.Prompt.PromptRevision}},
		SourceProvenance:   prompt.SourceProvenance,
	}
	if _, err := runtime.BindProjectTarget(binding); err != nil {
		t.Fatalf("bind test Project Target: %v", err)
	}
	ctx, err := runtime.PrepareProjectExecution(context.Background(), &protocol.ExecutionContext{
		WorkSessionID:      binding.WorkSessionID,
		TargetID:           binding.TargetID,
		ProjectID:          binding.ProjectID,
		DeploymentID:       binding.DeploymentID,
		DeploymentRevision: binding.DeploymentRevision,
		ContextRevision:    binding.ContextRevision,
	})
	if err != nil {
		t.Fatalf("prepare test Project execution: %v", err)
	}
	return ctx
}
