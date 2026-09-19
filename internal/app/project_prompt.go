package app

import (
	"context"
	"sort"
	"strings"

	protocol "github.com/Serialeo/agentdock-protocol"
	projectstate "github.com/uvwt/agentdock/internal/project"
)

func (r *Runtime) LoadProjectPrompt(request protocol.ProjectPromptLoadRequest) (protocol.ProjectPromptLoadResult, error) {
	if r == nil || r.projects == nil || r.projectInstructions == nil {
		return protocol.ProjectPromptLoadResult{}, &protocol.RemoteError{Code: protocol.ErrorDeploymentNotReady, Message: "Project Prompt loader is not initialized", Category: "runtime"}
	}
	deployment, err := r.projectDeploymentForPrompt(request.DeploymentID, request.DeploymentRevision)
	if err != nil {
		return protocol.ProjectPromptLoadResult{}, err
	}
	result, err := r.projectInstructions.Load(deployment, request.CWDRel)
	if err != nil {
		return protocol.ProjectPromptLoadResult{}, err
	}
	if strings.TrimSpace(deployment.WorkingFolder) == "" {
		result.SourceProvenance = protocol.SourceProvenance{Kind: protocol.SourceProvenanceNone}
		return result, nil
	}
	provenance, err := projectstate.InspectSourceProvenance(context.Background(), deployment.WorkingFolder)
	if err != nil {
		return protocol.ProjectPromptLoadResult{}, &protocol.RemoteError{
			Code: protocol.ErrorDeploymentNotReady, Message: "Project source provenance could not be inspected", Category: "runtime",
			Details: map[string]any{"deployment_id": deployment.ID, "reason": err.Error()},
		}
	}
	result.SourceProvenance = provenance
	return result, nil
}

func (r *Runtime) WriteProjectPrompt(request protocol.ProjectPromptWriteRequest) (protocol.ProjectPromptWriteResult, error) {
	if r == nil || r.projects == nil || r.projectInstructions == nil {
		return protocol.ProjectPromptWriteResult{}, &protocol.RemoteError{Code: protocol.ErrorDeploymentNotReady, Message: "Project Prompt loader is not initialized", Category: "runtime"}
	}
	deployment, err := r.projectDeploymentForPrompt(request.DeploymentID, request.DeploymentRevision)
	if err != nil {
		return protocol.ProjectPromptWriteResult{}, err
	}
	return r.projectInstructions.Write(deployment, request)
}

func (r *Runtime) projectDeploymentForPrompt(deploymentID, deploymentRevision string) (protocol.Deployment, error) {
	deploymentID = strings.TrimSpace(deploymentID)
	deploymentRevision = strings.TrimSpace(deploymentRevision)
	if deploymentID == "" || deploymentRevision == "" {
		return protocol.Deployment{}, &protocol.RemoteError{
			Code: protocol.ErrorExecutionContextInvalid, Message: "Project Prompt request requires deployment_id and deployment_revision", Category: "validation",
		}
	}
	deployment, ok := r.projects.Deployment(deploymentID)
	if !ok || !deployment.Enabled || deployment.ApplyStatus != protocol.DeploymentApplyApplied {
		return protocol.Deployment{}, &protocol.RemoteError{
			Code: protocol.ErrorDeploymentNotReady, Message: "Deployment is not enabled and applied for Project Prompt loading", Category: "authorization",
			Details: map[string]any{"deployment_id": deploymentID},
		}
	}
	if deployment.AppliedRevision != deploymentRevision {
		return protocol.Deployment{}, &protocol.RemoteError{
			Code: protocol.ErrorRevisionConflict, Message: "Deployment revision is stale for Project Prompt loading", Category: "conflict",
			Details: map[string]any{"deployment_id": deploymentID, "applied_revision": deployment.AppliedRevision},
		}
	}
	return deployment, nil
}

func (r *Runtime) validateProjectPromptScopes(deployment protocol.Deployment, cwdRel string, scopes []protocol.PromptScopeRevision) ([]protocol.PromptScopeRevision, error) {
	if len(scopes) == 0 {
		return nil, &protocol.RemoteError{
			Code: protocol.ErrorContextRefreshRequired, Message: "Target has no delivered Project Prompt scopes", Category: "conflict",
			Details: map[string]any{"deployment_id": deployment.ID},
		}
	}

	normalized := make([]protocol.PromptScopeRevision, 0, len(scopes))
	byScope := make(map[string]string, len(scopes))
	deliveredRevisions := make(map[string]struct{}, len(scopes))
	uniqueSources := make(map[string]int)
	sourceHashes := make(map[string]string)
	totalBytes := 0
	for index, scope := range scopes {
		revision := strings.TrimSpace(scope.PromptRevision)
		requestedScope := strings.TrimSpace(scope.Scope)
		if requestedScope == "" {
			requestedScope = "."
		}
		if revision == "" {
			return nil, &protocol.RemoteError{
				Code: protocol.ErrorExecutionContextInvalid, Message: "Project Prompt scope revision is required", Category: "validation",
				Details: map[string]any{"field": "prompt_scopes", "index": index},
			}
		}
		loaded, err := r.projectInstructions.Load(deployment, requestedScope)
		if err != nil {
			return nil, err
		}
		currentRevision := loaded.Prompt.PromptRevision
		if currentRevision != revision {
			return nil, &protocol.RemoteError{
				Code: protocol.ErrorContextRefreshRequired, Message: "delivered Project Prompt scope is stale", Category: "conflict",
				Details: map[string]any{"scope": loaded.CWDRel},
			}
		}
		if existing, exists := byScope[loaded.CWDRel]; exists {
			if existing != revision {
				return nil, &protocol.RemoteError{Code: protocol.ErrorExecutionContextInvalid, Message: "duplicate Project Prompt scope has conflicting revisions", Category: "validation", Details: map[string]any{"scope": loaded.CWDRel}}
			}
			continue
		}
		byScope[loaded.CWDRel] = revision
		deliveredRevisions[revision] = struct{}{}
		normalized = append(normalized, protocol.PromptScopeRevision{Scope: loaded.CWDRel, PromptRevision: revision})
		for _, source := range loaded.Prompt.Sources {
			if previous, exists := sourceHashes[source.Path]; exists && previous != source.SHA256 {
				return nil, &protocol.RemoteError{
					Code: protocol.ErrorContextRefreshRequired, Message: "Project Prompt source changed while validating delivered scopes", Category: "conflict",
					Details: map[string]any{"path": source.Path},
				}
			}
			sourceHashes[source.Path] = source.SHA256
			key := source.Path + "\x00" + source.SHA256
			if _, exists := uniqueSources[key]; exists {
				continue
			}
			uniqueSources[key] = source.Bytes
			totalBytes += source.Bytes
			if totalBytes > protocol.MaxProjectPromptContextBytes {
				return nil, &protocol.RemoteError{
					Code: protocol.ErrorProjectPromptTooLarge, Message: "delivered Project Prompt scopes exceed the context byte budget", Category: "resource_limit",
					Details: map[string]any{"bytes": totalBytes, "max_bytes": protocol.MaxProjectPromptContextBytes},
				}
			}
		}
	}

	current, err := r.projectInstructions.Load(deployment, cwdRel)
	if err != nil {
		return nil, err
	}
	if _, delivered := deliveredRevisions[current.Prompt.PromptRevision]; !delivered {
		return nil, &protocol.RemoteError{
			Code: protocol.ErrorContextRefreshRequired, Message: "Target cwd Project Prompt has not been delivered", Category: "conflict",
			Details: map[string]any{"required_scope": current.CWDRel},
		}
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].Scope < normalized[j].Scope })
	return normalized, nil
}

func (r *Runtime) verifyProjectExecutionPrompts(execution projectstate.Execution) error {
	_, err := r.validateProjectPromptScopes(execution.Deployment, execution.Target.CWDRel, execution.Target.PromptScopes)
	return err
}

func (r *Runtime) verifyProjectSourceProvenance(deployment protocol.Deployment, expected protocol.SourceProvenance) error {
	if err := expected.Validate(); err != nil {
		return &protocol.RemoteError{
			Code: protocol.ErrorExecutionContextInvalid, Message: "Target source provenance is invalid", Category: "validation",
			Details: map[string]any{"deployment_id": deployment.ID, "reason": err.Error()},
		}
	}
	if strings.TrimSpace(deployment.WorkingFolder) == "" {
		if expected.Kind != protocol.SourceProvenanceNone {
			return &protocol.RemoteError{
				Code: protocol.ErrorContextRefreshRequired, Message: "Folderless Project Target source provenance changed; refresh Target context before continuing", Category: "conflict",
				Details: map[string]any{"deployment_id": deployment.ID, "expected": expected, "current": protocol.SourceProvenance{Kind: protocol.SourceProvenanceNone}},
			}
		}
		return nil
	}
	current, err := projectstate.InspectSourceProvenance(context.Background(), deployment.WorkingFolder)
	if err != nil {
		return &protocol.RemoteError{
			Code: protocol.ErrorDeploymentNotReady, Message: "Project source provenance could not be inspected", Category: "runtime",
			Details: map[string]any{"deployment_id": deployment.ID, "reason": err.Error()},
		}
	}
	if current != expected {
		return &protocol.RemoteError{
			Code: protocol.ErrorContextRefreshRequired, Message: "Project source provenance changed; refresh Target context before continuing", Category: "conflict",
			Details: map[string]any{
				"deployment_id": deployment.ID,
				"expected":      expected,
				"current":       current,
			},
		}
	}
	return nil
}

func promptRevisionDelivered(scopes []protocol.PromptScopeRevision, revision string) bool {
	for _, scope := range scopes {
		if scope.PromptRevision == revision {
			return true
		}
	}
	return false
}
