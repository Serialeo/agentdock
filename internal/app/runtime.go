package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	protocol "github.com/Serialeo/agentdock-protocol"
	acpruntime "github.com/uvwt/agentdock/internal/acp"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/envstore"
	"github.com/uvwt/agentdock/internal/evolution"
	projectinstructions "github.com/uvwt/agentdock/internal/instructions"
	mcpclient "github.com/uvwt/agentdock/internal/mcp/client"
	projectstate "github.com/uvwt/agentdock/internal/project"
	"github.com/uvwt/agentdock/internal/taskstate"
	toolacp "github.com/uvwt/agentdock/internal/tool/acp"
	toolbrowser "github.com/uvwt/agentdock/internal/tool/browser"
	toolcommand "github.com/uvwt/agentdock/internal/tool/command"
	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
	toolcore "github.com/uvwt/agentdock/internal/tool/core"
	toolfile "github.com/uvwt/agentdock/internal/tool/file"
	toolmcp "github.com/uvwt/agentdock/internal/tool/mcp"
	toolmedia "github.com/uvwt/agentdock/internal/tool/media"
	toolrecall "github.com/uvwt/agentdock/internal/tool/recall"
	toolskill "github.com/uvwt/agentdock/internal/tool/skill"
	tooltask "github.com/uvwt/agentdock/internal/tool/task"
	"github.com/uvwt/agentdock/internal/workspace"
)

type Result = toolcore.Result

type Runtime struct {
	cfg                 config.Config
	toolNames           []string
	toolValidators      map[string]*toolcontract.InputValidator
	ws                  *workspace.Workspace
	projects            *projectstate.Store
	projectInstructions *projectinstructions.Loader
	skills              *toolskill.Service
	command             *toolcommand.Service
	files               *toolfile.Service
	dynamicMCP          *toolmcp.Service
	media               *toolmedia.Service
	browser             *toolbrowser.Service
	browserOwnerMu      sync.RWMutex
	browserOwners       map[string]browserSessionOwner
	recall              *toolrecall.Service
	evolution           *evolution.Service
	taskTools           *tooltask.Service
	acp                 *toolacp.Service
	lifecycleMu         sync.RWMutex
	commandCtx          context.Context
	commandCancel       context.CancelFunc
	closing             bool
	closeOnce           sync.Once
	closeErr            error
}

func NewRuntime(cfg config.Config) (*Runtime, error) {
	toolNames, toolValidators, err := compileAvailableToolContracts(cfg)
	if err != nil {
		return nil, fmt.Errorf("initialize tool contracts: %w", err)
	}
	ws, err := workspace.New(cfg.AgentDockDefaultDir)
	if err != nil {
		return nil, err
	}
	projects, err := projectstate.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("initialize Project execution state: %w", err)
	}
	envs, err := envstore.New(cfg.AgentDockHome)
	if err != nil {
		return nil, err
	}
	skills, err := toolskill.New(cfg, ws, envs)
	if err != nil {
		return nil, err
	}
	mcpClients, err := mcpclient.NewManager(cfg.AgentDockHome, envs)
	if err != nil {
		return nil, err
	}
	tasks, err := taskstate.New(filepath.Join(cfg.AgentDockHome, "tasks"))
	if err != nil {
		_ = mcpClients.Close()
		return nil, err
	}
	commandCtx, commandCancel := context.WithCancel(context.Background())
	runtime := &Runtime{
		cfg: cfg, ws: ws, projects: projects, projectInstructions: projectinstructions.NewLoader(cfg.AgentDockDefaultDir), skills: skills,
		toolNames: toolNames, toolValidators: toolValidators,
		commandCtx: commandCtx, commandCancel: commandCancel,
		browserOwners: make(map[string]browserSessionOwner),
	}
	journal, err := toolcommand.NewJournal(cfg.AgentDockHome)
	if err != nil {
		commandCancel()
		_ = mcpClients.Close()
		return nil, fmt.Errorf("initialize durable command journal: %w", err)
	}
	runtime.command = toolcommand.New(func() config.Config { return runtime.cfg }, ws, envs, skills.ResolveActive, runtime.commandExecutionContext, journal)
	runtime.files = toolfile.New(ws, skills.ResolveResource, runtime.command.CommandEnv)
	runtime.dynamicMCP = toolmcp.New(mcpClients, envs)
	runtime.media = toolmedia.New(cfg, ws, runtime.command.InternalCommandEnv)
	runtime.browser = toolbrowser.New(
		toolbrowser.Config{AgentDockHome: cfg.AgentDockHome, ExecutablePath: cfg.BrowserExecutablePath, CDPURL: cfg.BrowserCDPURL, ReuseExistingCDP: cfg.BrowserReuseExistingCDP},
		runtime.media.PublishBrowserScreenshot,
	)
	runtime.recall = toolrecall.New(func() config.Config { return runtime.cfg })
	runtime.evolution = evolution.New(func() config.Config { return runtime.cfg }, tasks)
	runtime.taskTools = tooltask.New(func() config.Config { return runtime.cfg }, tasks, runtime.evolution)
	if cfg.ACPEnabled {
		acpEnvironment := make(map[string]string, len(cfg.ACPEnvFromEnv))
		for childName, hostName := range cfg.ACPEnvFromEnv {
			value, exists := os.LookupEnv(hostName)
			if !exists {
				_ = runtime.Close()
				return nil, fmt.Errorf("required ACP environment variable %s is missing", hostName)
			}
			acpEnvironment[childName] = value
		}
		manager, err := acpruntime.NewManager(acpruntime.Options{
			Home:       cfg.AgentDockHome,
			DefaultCWD: cfg.AgentDockDefaultDir,
			Agent: acpruntime.AgentSpec{
				Name: cfg.ACPAgentName, Command: cfg.ACPCommand, Args: append([]string(nil), cfg.ACPArgs...), Environment: acpEnvironment,
			},
			MaxConcurrentRuns:  cfg.ACPMaxPrompts,
			InteractionTimeout: time.Duration(cfg.ACPInteractionMS) * time.Millisecond,
		})
		if err != nil {
			_ = runtime.Close()
			return nil, fmt.Errorf("initialize ACP runtime: %w", err)
		}
		runtime.acp = toolacp.New(manager, ws)
	}
	return runtime, nil
}

func (r *Runtime) Config() config.Config           { return r.cfg }
func (r *Runtime) Workspace() *workspace.Workspace { return r.ws }

func (r *Runtime) ApplyProjectDeployment(deployment protocol.Deployment) (protocol.Deployment, error) {
	if r == nil || r.projects == nil {
		return protocol.Deployment{}, errors.New("Project execution state is not initialized")
	}
	// P1 understands policy but does not yet advertise a production native backend.
	if deployment.Permissions.Computer != protocol.ComputerPermissionNone {
		return protocol.Deployment{}, toolErrorDetails(protocol.ErrorComputerUnsupported, "native computer backend is not integrated in this Node build", "capability", map[string]any{"required_capability": protocol.ComputerCapability})
	}
	return r.projects.ApplyDeployment(deployment)
}

func (r *Runtime) RemoveProjectDeployment(deploymentID string) error {
	if r == nil || r.projects == nil {
		return errors.New("Project execution state is not initialized")
	}
	return r.projects.RemoveDeployment(deploymentID)
}

func (r *Runtime) BindProjectTarget(request protocol.ProjectTargetBindRequest) (projectstate.TargetBinding, error) {
	if r == nil || r.projects == nil || r.projectInstructions == nil {
		return projectstate.TargetBinding{}, errors.New("Project execution state is not initialized")
	}
	deployment, err := r.projectDeploymentForPrompt(request.DeploymentID, request.DeploymentRevision)
	if err != nil {
		return projectstate.TargetBinding{}, err
	}
	scopes, err := r.validateProjectPromptScopes(deployment, request.CWDRel, request.PromptScopes)
	if err != nil {
		return projectstate.TargetBinding{}, err
	}
	if err := r.verifyProjectSourceProvenance(deployment, request.SourceProvenance); err != nil {
		return projectstate.TargetBinding{}, err
	}
	request.PromptScopes = scopes
	return r.projects.BindTarget(request)
}

func (r *Runtime) RebindProjectTarget(request protocol.ProjectTargetRebindRequest) (projectstate.TargetBinding, error) {
	if r == nil || r.projects == nil || r.projectInstructions == nil {
		return projectstate.TargetBinding{}, errors.New("Project execution state is not initialized")
	}
	binding, ok := r.projects.Target(request.TargetID)
	if !ok || binding.WorkSessionID != strings.TrimSpace(request.WorkSessionID) {
		return projectstate.TargetBinding{}, &protocol.RemoteError{Code: protocol.ErrorSessionTargetDenied, Message: "Target is not bound to this WorkSession", Category: "authorization", Details: map[string]any{"target_id": request.TargetID}}
	}
	deployment, err := r.projectDeploymentForPrompt(binding.DeploymentID, binding.DeploymentRevision)
	if err != nil {
		return projectstate.TargetBinding{}, err
	}
	scopes, err := r.validateProjectPromptScopes(deployment, request.CWDRel, request.PromptScopes)
	if err != nil {
		return projectstate.TargetBinding{}, err
	}
	if err := r.verifyProjectSourceProvenance(deployment, request.SourceProvenance); err != nil {
		return projectstate.TargetBinding{}, err
	}
	request.PromptScopes = scopes
	return r.projects.RebindTarget(request)
}

func (r *Runtime) RevokeProjectTarget(targetID string) {
	if r != nil && r.projects != nil {
		r.projects.RevokeTarget(targetID)
	}
}

func (r *Runtime) RevokeProjectSession(workSessionID string) {
	if r != nil && r.projects != nil {
		r.projects.RevokeSession(workSessionID)
	}
}

func (r *Runtime) PrepareProjectExecution(ctx context.Context, executionContext *protocol.ExecutionContext) (context.Context, error) {
	if r == nil || r.projects == nil || r.ws == nil {
		return nil, errors.New("Project execution state is not initialized")
	}
	execution, err := r.projects.ResolveExecution(executionContext)
	if err != nil {
		return nil, err
	}
	if err := r.verifyProjectExecutionPrompts(execution); err != nil {
		return nil, err
	}
	if err := r.verifyProjectSourceProvenance(execution.Deployment, execution.Target.SourceProvenance); err != nil {
		return nil, err
	}
	return r.bindPreparedProjectExecution(ctx, execution)
}

func (r *Runtime) PrepareProjectSessionControlExecution(ctx context.Context, executionContext *protocol.ExecutionContext) (context.Context, error) {
	if r == nil || r.projects == nil || r.ws == nil {
		return nil, errors.New("Project execution state is not initialized")
	}
	execution, err := r.projects.ResolveSessionControlExecution(executionContext)
	if err != nil {
		return nil, err
	}
	return r.bindPreparedProjectExecution(ctx, execution)
}

func (r *Runtime) bindPreparedProjectExecution(ctx context.Context, execution projectstate.Execution) (context.Context, error) {
	ctx = projectstate.WithExecution(ctx, execution)
	return workspace.WithCWD(ctx, execution.CWD)
}

func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		var closeErrors []error
		r.lifecycleMu.Lock()
		r.closing = true
		commandCancel := r.commandCancel
		r.lifecycleMu.Unlock()

		// 先禁止新的 command reservation，并等已经拿到 reservation 的启动流程离开
		// cmd.Start/平台进程控制器建立窗口。此处不能持有 lifecycleMu 等待，否则启动路径
		// 一旦需要读取 Runtime 生命周期状态就会形成锁顺序死锁。
		if r.command != nil {
			r.command.BeginClose()
			if err := r.command.WaitForStarts(); err != nil {
				closeErrors = append(closeErrors, err)
			}
		}
		if commandCancel != nil {
			commandCancel()
		}
		if r.acp != nil {
			if err := r.acp.Close(); err != nil {
				closeErrors = append(closeErrors, fmt.Errorf("close ACP runtime: %w", err))
			}
		}
		if r.browser != nil {
			if err := r.browser.Close(); err != nil {
				closeErrors = append(closeErrors, fmt.Errorf("close browser runtime: %w", err))
			}
		}
		if r.command != nil {
			if err := r.command.Close(); err != nil {
				closeErrors = append(closeErrors, err)
			}
		}
		if r.dynamicMCP != nil {
			if err := r.dynamicMCP.Close(); err != nil {
				closeErrors = append(closeErrors, fmt.Errorf("close dynamic MCP clients: %w", err))
			}
		}
		r.closeErr = errors.Join(closeErrors...)
	})
	return r.closeErr
}

func (r *Runtime) commandExecutionContext() (context.Context, error) {
	r.lifecycleMu.RLock()
	defer r.lifecycleMu.RUnlock()
	if r.closing || r.commandCtx == nil {
		return nil, toolError("RUNTIME_CLOSING", "AgentDock runtime is shutting down", "runtime")
	}
	return r.commandCtx, nil
}

func (r *Runtime) ToolNames() []string {
	return append([]string(nil), r.toolNames...)
}

func (r *Runtime) ToolDefinitions() []ToolDefinition {
	definitions := make([]ToolDefinition, 0, len(r.toolNames))
	for _, name := range r.toolNames {
		definition, _ := toolDefinitionForConfig(name, r.cfg)
		definitions = append(definitions, definition)
	}
	return definitions
}

func (r *Runtime) ToolDefinition(name string) (ToolDefinition, bool) {
	if _, available := r.toolValidators[name]; !available {
		return ToolDefinition{}, false
	}
	return toolDefinitionForConfig(name, r.cfg)
}

func (r *Runtime) Call(ctx context.Context, name string, args map[string]any) (Result, error) {
	if args == nil {
		args = map[string]any{}
	}
	if err := r.validateToolArguments(name, args); err != nil {
		return nil, err
	}
	var err error
	ctx, err = r.refreshPreparedProjectExecution(ctx, name, args)
	if err != nil {
		return nil, err
	}
	spec, ok := toolSpecByName(name)
	if !ok || spec.Handler == nil {
		return nil, toolErrorDetails("UNKNOWN_TOOL", "tool has no handler", "validation", map[string]any{"tool": name})
	}
	if err := r.authorizeProjectTool(ctx, name, args); err != nil {
		return nil, err
	}
	if err := r.ensureProjectPromptForTool(ctx, name, args); err != nil {
		return nil, err
	}
	return spec.Handler(ctx, r, args)
}

func (r *Runtime) refreshPreparedProjectExecution(ctx context.Context, toolName string, arguments ...map[string]any) (context.Context, error) {
	execution, ok := projectstate.ExecutionFromContext(ctx)
	if !ok || r == nil || r.projects == nil || r.ws == nil {
		return ctx, nil
	}
	executionContext := execution.Context
	var refreshed projectstate.Execution
	var err error
	var args map[string]any
	if len(arguments) > 0 {
		args = arguments[0]
	}
	switch {
	case protocol.AllowsHistoricalComputerControl(toolName, args):
		refreshed, err = r.projects.ResolveSessionControlExecution(&executionContext)
	case toolName == "session_observe" || toolName == "session_act" || toolName == "acp_session" || toolName == "acp_prompt" || toolName == "acp_interaction":
		refreshed, err = r.projects.ResolveSessionControlExecution(&executionContext)
	default:
		refreshed, err = r.projects.ResolveExecution(&executionContext)
	}
	if err != nil {
		return nil, err
	}
	return r.bindPreparedProjectExecution(ctx, refreshed)
}

func (r *Runtime) validateToolArguments(name string, args map[string]any) error {
	validator, available := r.toolValidators[name]
	if !available {
		return toolErrorDetails("UNKNOWN_TOOL", "tool is not available", "validation", map[string]any{"tool": name})
	}
	if args == nil {
		args = map[string]any{}
	}
	if err := validator.Validate(args); err != nil {
		return toolErrorDetails(
			"INVALID_ARGUMENT",
			"tool arguments do not match the declared input schema",
			"validation",
			map[string]any{"tool": name, "reason": toolcontract.CompactValidationError(err)},
		)
	}
	return nil
}
