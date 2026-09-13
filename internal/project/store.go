package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	protocol "github.com/Serialeo/agentdock-protocol"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/fs/atomicfile"
)

const (
	stateVersion  = 1
	maxStateBytes = 2 << 20
)

type diskState struct {
	Version     int                   `json:"version"`
	Deployments []protocol.Deployment `json:"deployments"`
}

type TargetBinding struct {
	WorkSessionID      string                         `json:"work_session_id"`
	TargetID           string                         `json:"target_id"`
	ProjectID          string                         `json:"project_id"`
	DeploymentID       string                         `json:"deployment_id"`
	CWDRel             string                         `json:"cwd_rel"`
	CWDAbs             string                         `json:"-"`
	DeploymentRevision string                         `json:"deployment_revision"`
	ContextRevision    string                         `json:"context_revision"`
	PromptScopes       []protocol.PromptScopeRevision `json:"prompt_scopes"`
	SourceProvenance   protocol.SourceProvenance      `json:"source_provenance"`
	Revoked            bool                           `json:"revoked"`
}

type BindTargetRequest = protocol.ProjectTargetBindRequest

type Execution struct {
	Context     protocol.ExecutionContext
	Deployment  protocol.Deployment
	Target      TargetBinding
	CWD         string
	Permissions protocol.DeploymentPermissions
}

type Store struct {
	mu          sync.RWMutex
	path        string
	defaultCWD  string
	deployments map[string]protocol.Deployment
	targets     map[string]TargetBinding
}

func New(cfg config.Config) (*Store, error) {
	defaultCWD := strings.TrimSpace(cfg.AgentDockDefaultDir)
	if defaultCWD == "" {
		defaultCWD = strings.TrimSpace(cfg.AgentDockHome)
	}
	if defaultCWD == "" {
		var err error
		defaultCWD, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("resolve AgentDock default cwd: %w", err)
		}
	}
	defaultCWD = filepath.Clean(defaultCWD)
	store := &Store{
		path:        filepath.Join(cfg.AgentDockHome, "projects", "applied-deployments-v1.json"),
		defaultCWD:  defaultCWD,
		deployments: make(map[string]protocol.Deployment),
		targets:     make(map[string]TargetBinding),
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) load() error {
	info, err := os.Lstat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat applied Project state: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("applied Project state must be a regular file")
	}
	if info.Size() > maxStateBytes {
		return fmt.Errorf("applied Project state exceeds %d bytes", maxStateBytes)
	}
	file, err := os.Open(s.path)
	if err != nil {
		return fmt.Errorf("open applied Project state: %w", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxStateBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return fmt.Errorf("read applied Project state: %w", readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close applied Project state: %w", closeErr)
	}
	if len(data) > maxStateBytes {
		return fmt.Errorf("applied Project state exceeds %d bytes", maxStateBytes)
	}
	var state diskState
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return fmt.Errorf("decode applied Project state: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("applied Project state must contain exactly one JSON value")
	}
	if state.Version != stateVersion {
		return fmt.Errorf("unsupported applied Project state version: %d", state.Version)
	}
	for _, deployment := range state.Deployments {
		normalized, err := validateDeployment(deployment)
		if err != nil {
			return fmt.Errorf("validate persisted Deployment %q: %w", deployment.ID, err)
		}
		if _, exists := s.deployments[normalized.ID]; exists {
			return fmt.Errorf("duplicate persisted Deployment id: %s", normalized.ID)
		}
		s.deployments[normalized.ID] = normalized
	}
	return nil
}

func (s *Store) ApplyDeployment(deployment protocol.Deployment) (protocol.Deployment, error) {
	normalized, err := validateDeployment(deployment)
	if err != nil {
		return protocol.Deployment{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	next := cloneDeployments(s.deployments)
	next[normalized.ID] = normalized
	if err := s.persistLocked(next); err != nil {
		return protocol.Deployment{}, err
	}
	s.deployments = next
	return normalized, nil
}

func (s *Store) RemoveDeployment(deploymentID string) error {
	deploymentID = strings.TrimSpace(deploymentID)
	if deploymentID == "" {
		return remoteError(protocol.ErrorExecutionContextInvalid, "deployment_id is required", "validation", map[string]any{"field": "deployment_id"})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.deployments[deploymentID]; !exists {
		return nil
	}
	next := cloneDeployments(s.deployments)
	delete(next, deploymentID)
	if err := s.persistLocked(next); err != nil {
		return err
	}
	s.deployments = next
	for id, target := range s.targets {
		if target.DeploymentID == deploymentID {
			target.Revoked = true
			s.targets[id] = target
		}
	}
	return nil
}

func (s *Store) Deployment(deploymentID string) (protocol.Deployment, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	deployment, ok := s.deployments[deploymentID]
	return deployment, ok
}

func (s *Store) Target(targetID string) (TargetBinding, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	binding, ok := s.targets[strings.TrimSpace(targetID)]
	if ok {
		binding.PromptScopes = clonePromptScopes(binding.PromptScopes)
	}
	return binding, ok
}

func (s *Store) BindTarget(request BindTargetRequest) (TargetBinding, error) {
	if err := validateBindingFields(request); err != nil {
		return TargetBinding{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	deployment, ok := s.deployments[request.DeploymentID]
	if !ok || deployment.ProjectID != request.ProjectID {
		return TargetBinding{}, remoteError(protocol.ErrorDeploymentNotReady, "Deployment is not applied on this node", "authorization", map[string]any{"deployment_id": request.DeploymentID})
	}
	if !deployment.Enabled || deployment.ApplyStatus != protocol.DeploymentApplyApplied {
		return TargetBinding{}, remoteError(protocol.ErrorDeploymentNotReady, "Deployment is not enabled and applied", "authorization", map[string]any{"deployment_id": request.DeploymentID, "apply_status": deployment.ApplyStatus})
	}
	if deployment.AppliedRevision != request.DeploymentRevision {
		return TargetBinding{}, remoteError(protocol.ErrorRevisionConflict, "Deployment revision does not match the applied node configuration", "conflict", map[string]any{"deployment_id": request.DeploymentID, "applied_revision": deployment.AppliedRevision})
	}
	cwdRel, cwdAbs, err := s.resolveTargetCWD(deployment.WorkingFolder, request.CWDRel)
	if err != nil {
		return TargetBinding{}, err
	}
	binding := TargetBinding{
		WorkSessionID:      request.WorkSessionID,
		TargetID:           request.TargetID,
		ProjectID:          request.ProjectID,
		DeploymentID:       request.DeploymentID,
		CWDRel:             cwdRel,
		CWDAbs:             cwdAbs,
		DeploymentRevision: request.DeploymentRevision,
		ContextRevision:    request.ContextRevision,
		PromptScopes:       clonePromptScopes(request.PromptScopes),
		SourceProvenance:   request.SourceProvenance,
	}
	if existing, exists := s.targets[binding.TargetID]; exists {
		if sameBinding(existing, binding) && !existing.Revoked {
			return existing, nil
		}
		return TargetBinding{}, remoteError(protocol.ErrorSessionTargetDenied, "Target id is already bound to different execution context", "authorization", map[string]any{"target_id": binding.TargetID})
	}
	s.targets[binding.TargetID] = binding
	return binding, nil
}

func (s *Store) RebindTarget(request protocol.ProjectTargetRebindRequest) (TargetBinding, error) {
	workSessionID := strings.TrimSpace(request.WorkSessionID)
	targetID := strings.TrimSpace(request.TargetID)
	contextRevision := strings.TrimSpace(request.ContextRevision)
	if workSessionID == "" || targetID == "" || contextRevision == "" || len(request.PromptScopes) == 0 {
		return TargetBinding{}, remoteError(protocol.ErrorExecutionContextInvalid, "target rebind context is incomplete", "validation", nil)
	}
	if err := request.SourceProvenance.Validate(); err != nil {
		return TargetBinding{}, remoteError(protocol.ErrorExecutionContextInvalid, "target rebind source provenance is invalid", "validation", map[string]any{"field": "source_provenance", "reason": err.Error()})
	}
	if request.SourceProvenance.Kind == protocol.SourceProvenanceUnknown {
		return TargetBinding{}, remoteError(protocol.ErrorExecutionContextInvalid, "target rebind requires inspected source provenance", "validation", map[string]any{"field": "source_provenance"})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	binding, ok := s.targets[targetID]
	if !ok || binding.Revoked || binding.WorkSessionID != workSessionID {
		return TargetBinding{}, remoteError(protocol.ErrorSessionTargetDenied, "Target is not bound to this WorkSession", "authorization", map[string]any{"target_id": targetID})
	}
	deployment, ok := s.deployments[binding.DeploymentID]
	if !ok || !deployment.Enabled || deployment.AppliedRevision != binding.DeploymentRevision {
		return TargetBinding{}, remoteError(protocol.ErrorDeploymentNotReady, "Deployment changed while rebinding Target", "authorization", map[string]any{"deployment_id": binding.DeploymentID})
	}
	normalizedRel, cwdAbs, err := s.resolveTargetCWD(deployment.WorkingFolder, request.CWDRel)
	if err != nil {
		return TargetBinding{}, err
	}
	contextChanged := normalizedRel != binding.CWDRel || !equalPromptScopes(binding.PromptScopes, request.PromptScopes)
	if contextChanged && contextRevision == binding.ContextRevision {
		return TargetBinding{}, remoteError(protocol.ErrorRevisionConflict, "context_revision must change when Target Prompt context changes", "conflict", map[string]any{"target_id": targetID})
	}
	binding.CWDRel = normalizedRel
	binding.CWDAbs = cwdAbs
	binding.ContextRevision = contextRevision
	binding.PromptScopes = clonePromptScopes(request.PromptScopes)
	binding.SourceProvenance = request.SourceProvenance
	s.targets[targetID] = binding
	return binding, nil
}

func (s *Store) RevokeTarget(targetID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if target, ok := s.targets[strings.TrimSpace(targetID)]; ok {
		target.Revoked = true
		s.targets[target.TargetID] = target
	}
}

func (s *Store) RevokeSession(workSessionID string) {
	workSessionID = strings.TrimSpace(workSessionID)
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, target := range s.targets {
		if target.WorkSessionID == workSessionID {
			target.Revoked = true
			s.targets[id] = target
		}
	}
}

func (s *Store) ResolveExecution(context *protocol.ExecutionContext) (Execution, error) {
	if validation := protocol.ValidateExecutionContext(context); validation != nil {
		return Execution{}, validation
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	binding, ok := s.targets[context.TargetID]
	if !ok || binding.Revoked {
		return Execution{}, remoteError(protocol.ErrorSessionTargetDenied, "Target is not active on this node", "authorization", map[string]any{"target_id": context.TargetID})
	}
	if binding.WorkSessionID != context.WorkSessionID || binding.ProjectID != context.ProjectID || binding.DeploymentID != context.DeploymentID {
		return Execution{}, remoteError(protocol.ErrorSessionTargetDenied, "execution context does not match the bound Target", "authorization", map[string]any{"target_id": context.TargetID})
	}
	if binding.DeploymentRevision != context.DeploymentRevision || binding.ContextRevision != context.ContextRevision {
		return Execution{}, remoteError(protocol.ErrorRevisionConflict, "execution context revision is stale", "conflict", map[string]any{"target_id": context.TargetID})
	}
	deployment, ok := s.deployments[binding.DeploymentID]
	if !ok || !deployment.Enabled || deployment.ApplyStatus != protocol.DeploymentApplyApplied {
		return Execution{}, remoteError(protocol.ErrorDeploymentNotReady, "bound Deployment is not enabled and applied", "authorization", map[string]any{"deployment_id": binding.DeploymentID})
	}
	if deployment.ProjectID != binding.ProjectID || deployment.AppliedRevision != binding.DeploymentRevision {
		return Execution{}, remoteError(protocol.ErrorRevisionConflict, "applied Deployment revision changed", "conflict", map[string]any{"deployment_id": binding.DeploymentID, "applied_revision": deployment.AppliedRevision})
	}
	return Execution{
		Context:     *context,
		Deployment:  deployment,
		Target:      binding,
		CWD:         binding.CWDAbs,
		Permissions: deployment.Permissions,
	}, nil
}

// ResolveSessionControlExecution accepts an exact historical Target binding even
// after the Deployment revision changed or the Target was revoked. It exists only
// so the owning WorkSession can observe or terminate commands that were already
// running. New execution still uses ResolveExecution and therefore rejects stale
// or revoked Targets.
func (s *Store) ResolveSessionControlExecution(context *protocol.ExecutionContext) (Execution, error) {
	if validation := protocol.ValidateExecutionContext(context); validation != nil {
		return Execution{}, validation
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	binding, ok := s.targets[context.TargetID]
	if !ok {
		return Execution{}, remoteError(protocol.ErrorSessionTargetDenied, "Target is not known on this node", "authorization", map[string]any{"target_id": context.TargetID})
	}
	if binding.WorkSessionID != context.WorkSessionID || binding.ProjectID != context.ProjectID || binding.DeploymentID != context.DeploymentID {
		return Execution{}, remoteError(protocol.ErrorSessionTargetDenied, "execution context does not match the bound Target", "authorization", map[string]any{"target_id": context.TargetID})
	}
	if binding.DeploymentRevision != context.DeploymentRevision || binding.ContextRevision != context.ContextRevision {
		return Execution{}, remoteError(protocol.ErrorRevisionConflict, "execution context does not match the bound Target revision", "conflict", map[string]any{"target_id": context.TargetID})
	}
	execution := Execution{Context: *context, Target: binding, CWD: binding.CWDAbs}
	if deployment, exists := s.deployments[binding.DeploymentID]; exists && deployment.ProjectID == binding.ProjectID {
		execution.Deployment = deployment
		execution.Permissions = deployment.Permissions
	}
	return execution, nil
}

func (s *Store) persistLocked(deployments map[string]protocol.Deployment) error {
	items := make([]protocol.Deployment, 0, len(deployments))
	for _, deployment := range deployments {
		items = append(items, deployment)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	encoded, err := json.MarshalIndent(diskState{Version: stateVersion, Deployments: items}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode applied Project state: %w", err)
	}
	if len(encoded) > maxStateBytes {
		return fmt.Errorf("applied Project state exceeds %d bytes", maxStateBytes)
	}
	if err := atomicfile.Write(s.path, append(encoded, '\n'), 0o600); err != nil {
		return fmt.Errorf("persist applied Project state: %w", err)
	}
	return nil
}

func validateDeployment(deployment protocol.Deployment) (protocol.Deployment, error) {
	deployment.ID = strings.TrimSpace(deployment.ID)
	deployment.ProjectID = strings.TrimSpace(deployment.ProjectID)
	deployment.NodeID = strings.TrimSpace(deployment.NodeID)
	deployment.WorkingFolder = strings.TrimSpace(deployment.WorkingFolder)
	if deployment.WorkingFolder != "" {
		deployment.WorkingFolder = filepath.Clean(deployment.WorkingFolder)
	}
	deployment.DesiredRevision = strings.TrimSpace(deployment.DesiredRevision)
	deployment.AppliedRevision = strings.TrimSpace(deployment.AppliedRevision)
	if deployment.ID == "" || deployment.ProjectID == "" || deployment.NodeID == "" || deployment.AppliedRevision == "" {
		return protocol.Deployment{}, remoteError(protocol.ErrorExecutionContextInvalid, "Deployment identity/revision is incomplete", "validation", nil)
	}
	if deployment.WorkingFolder != "" && !filepath.IsAbs(deployment.WorkingFolder) {
		return protocol.Deployment{}, remoteError(protocol.ErrorExecutionContextInvalid, "Deployment working_folder must be an absolute native path", "validation", map[string]any{"working_folder": deployment.WorkingFolder})
	}
	if err := deployment.Permissions.Validate(); err != nil {
		return protocol.Deployment{}, remoteError(protocol.ErrorExecutionContextInvalid, "Deployment permissions are invalid", "validation", map[string]any{"reason": err.Error()})
	}
	if deployment.Enabled && deployment.ApplyStatus != protocol.DeploymentApplyApplied {
		return protocol.Deployment{}, remoteError(protocol.ErrorExecutionContextInvalid, "enabled Deployment must have applied status", "validation", map[string]any{"apply_status": deployment.ApplyStatus})
	}
	if !deployment.Enabled && deployment.ApplyStatus != protocol.DeploymentApplyDisabled && deployment.ApplyStatus != protocol.DeploymentApplyApplied {
		return protocol.Deployment{}, remoteError(protocol.ErrorExecutionContextInvalid, "disabled Deployment has invalid apply status", "validation", map[string]any{"apply_status": deployment.ApplyStatus})
	}
	if deployment.WorkingFolder != "" {
		info, err := os.Stat(deployment.WorkingFolder)
		if err != nil {
			return protocol.Deployment{}, remoteError(protocol.ErrorDeploymentNotReady, "Deployment working_folder is unavailable", "runtime", map[string]any{"working_folder": deployment.WorkingFolder, "reason": err.Error()})
		}
		if !info.IsDir() {
			return protocol.Deployment{}, remoteError(protocol.ErrorDeploymentNotReady, "Deployment working_folder is not a directory", "validation", map[string]any{"working_folder": deployment.WorkingFolder})
		}
	}
	return deployment, nil
}

func validateBindingFields(request BindTargetRequest) error {
	fields := []struct {
		name  string
		value string
	}{
		{name: "work_session_id", value: request.WorkSessionID},
		{name: "target_id", value: request.TargetID},
		{name: "project_id", value: request.ProjectID},
		{name: "deployment_id", value: request.DeploymentID},
		{name: "deployment_revision", value: request.DeploymentRevision},
		{name: "context_revision", value: request.ContextRevision},
	}
	for _, field := range fields {
		if strings.TrimSpace(field.value) == "" {
			return remoteError(protocol.ErrorExecutionContextInvalid, "Target binding is incomplete", "validation", map[string]any{"field": field.name})
		}
	}
	if len(request.PromptScopes) == 0 {
		return remoteError(protocol.ErrorExecutionContextInvalid, "Target binding requires at least one delivered Project Prompt scope", "validation", map[string]any{"field": "prompt_scopes"})
	}
	if err := request.SourceProvenance.Validate(); err != nil {
		return remoteError(protocol.ErrorExecutionContextInvalid, "Target binding source provenance is invalid", "validation", map[string]any{"field": "source_provenance", "reason": err.Error()})
	}
	if request.SourceProvenance.Kind == protocol.SourceProvenanceUnknown {
		return remoteError(protocol.ErrorExecutionContextInvalid, "Target binding requires inspected source provenance", "validation", map[string]any{"field": "source_provenance"})
	}
	return nil
}

func (s *Store) resolveTargetCWD(root, raw string) (string, string, error) {
	configuredRoot := strings.TrimSpace(root)
	root = configuredRoot
	if root == "" {
		root = s.defaultCWD
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "."
	}
	if filepath.IsAbs(raw) || strings.HasPrefix(raw, "~") {
		return "", "", remoteError(protocol.ErrorPromptScopeEscape, "Target cwd must be relative to its configured Project context", "validation", map[string]any{"cwd_rel": raw})
	}
	clean := filepath.Clean(filepath.FromSlash(raw))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", "", remoteError(protocol.ErrorPromptScopeEscape, "Target cwd escapes its configured Project context", "validation", map[string]any{"cwd_rel": raw})
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", remoteError(protocol.ErrorDeploymentNotReady, "cannot resolve Project target cwd root", "runtime", map[string]any{"working_folder": configuredRoot, "reason": err.Error()})
	}
	candidate := filepath.Join(realRoot, clean)
	realCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", "", remoteError(protocol.ErrorDeploymentNotReady, "Target cwd is unavailable", "runtime", map[string]any{"cwd_rel": raw, "reason": err.Error()})
	}
	rel, err := filepath.Rel(realRoot, realCandidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", "", remoteError(protocol.ErrorPromptScopeEscape, "Target cwd resolves outside its configured Project context", "authorization", map[string]any{"cwd_rel": raw})
	}
	info, err := os.Stat(realCandidate)
	if err != nil || !info.IsDir() {
		return "", "", remoteError(protocol.ErrorDeploymentNotReady, "Target cwd is not an available directory", "runtime", map[string]any{"cwd_rel": raw})
	}
	return filepath.ToSlash(rel), realCandidate, nil
}

func sameBinding(a, b TargetBinding) bool {
	return a.WorkSessionID == b.WorkSessionID && a.TargetID == b.TargetID && a.ProjectID == b.ProjectID && a.DeploymentID == b.DeploymentID && a.CWDRel == b.CWDRel && a.CWDAbs == b.CWDAbs && a.DeploymentRevision == b.DeploymentRevision && a.ContextRevision == b.ContextRevision && a.SourceProvenance == b.SourceProvenance && equalPromptScopes(a.PromptScopes, b.PromptScopes)
}

func clonePromptScopes(input []protocol.PromptScopeRevision) []protocol.PromptScopeRevision {
	return append([]protocol.PromptScopeRevision(nil), input...)
}

func equalPromptScopes(a, b []protocol.PromptScopeRevision) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func cloneDeployments(input map[string]protocol.Deployment) map[string]protocol.Deployment {
	out := make(map[string]protocol.Deployment, len(input))
	for id, deployment := range input {
		out[id] = deployment
	}
	return out
}

func remoteError(code, message, category string, details map[string]any) *protocol.RemoteError {
	return &protocol.RemoteError{Code: code, Message: message, Category: category, Details: details}
}
