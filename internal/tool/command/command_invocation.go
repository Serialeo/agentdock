package command

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/envstore"
	projectstate "github.com/uvwt/agentdock/internal/project"
	"github.com/uvwt/agentdock/internal/tool/command/session"
)

type commandInvocation struct {
	command   string
	workdir   string
	env       []string
	build     session.CommandFactory
	execution session.ExecutionContext
}

type commandSkillContext struct {
	skill    string
	envSkill string
}

func (invocation commandInvocation) start(ctx context.Context, timeout time.Duration, tty bool, prepare session.PrepareFunc, options ...session.StartOptions) (*session.Session, session.PreparationStatus, error) {
	if invocation.build != nil {
		return session.StartCommandWithTTY(ctx, invocation.build, timeout, tty, prepare, options...)
	}
	return session.StartWithTTY(ctx, invocation.command, invocation.workdir, invocation.env, timeout, tty, prepare, options...)
}

func (svc *Service) prepareCommandInvocation(ctx context.Context, request ExecRequest) (commandInvocation, error) {
	return svc.newHostCommandInvocation(ctx, request)
}

func (svc *Service) newHostCommandInvocation(ctx context.Context, request ExecRequest) (commandInvocation, error) {
	skillContext, err := parseCommandSkillContext(request)
	if err != nil {
		return commandInvocation{}, err
	}
	workdir, err := svc.resolveHostCommandWorkdir(ctx, request.Workdir, skillContext.skill)
	if err != nil {
		return commandInvocation{}, err
	}
	info, err := os.Stat(workdir)
	if err != nil {
		return commandInvocation{}, err
	}
	if !info.IsDir() {
		return commandInvocation{}, toolError("NOT_A_DIRECTORY", "workdir is not a directory", "validation")
	}
	commandEnv, err := svc.commandEnv(skillContext.envSkill, request.Env)
	if err != nil {
		return commandInvocation{}, err
	}
	executionContext := session.ExecutionContext{Workdir: workdir}
	if execution, ok := projectstate.ExecutionFromContext(ctx); ok {
		executionContext.WorkSessionID = execution.Target.WorkSessionID
		executionContext.TargetID = execution.Target.TargetID
		executionContext.ProjectID = execution.Target.ProjectID
		executionContext.DeploymentID = execution.Target.DeploymentID
		executionContext.NodeID = execution.Deployment.NodeID
		executionContext.DeploymentRevision = execution.Target.DeploymentRevision
		executionContext.ContextRevision = execution.Target.ContextRevision
	}
	return commandInvocation{
		command:   request.Cmd,
		workdir:   workdir,
		env:       commandEnv,
		execution: executionContext,
	}, nil
}

func parseCommandSkillContext(request ExecRequest) (commandSkillContext, error) {
	skill := strings.TrimSpace(request.Skill)
	envSkill := strings.TrimSpace(request.SkillEnv)
	if skill != "" && envSkill != "" && skill != envSkill {
		return commandSkillContext{}, toolErrorDetails(
			"INVALID_ARGUMENT",
			"skill and skill_env must reference the same Skill when both are provided",
			"validation",
			map[string]any{"skill": skill, "skill_env": envSkill},
		)
	}
	if envSkill == "" {
		envSkill = skill
	}
	return commandSkillContext{skill: skill, envSkill: envSkill}, nil
}

func (svc *Service) resolveHostCommandWorkdir(ctx context.Context, requested string, skill string) (string, error) {
	skillDir := ""
	if skill != "" {
		var err error
		skillDir, err = svc.resolveSkillCommandDir(skill)
		if err != nil {
			return "", err
		}
	}
	if raw := strings.TrimSpace(requested); raw != "" {
		resolved, err := svc.ws.ResolveExistingContext(ctx, raw)
		if err != nil {
			return "", err
		}
		return resolved.Abs, nil
	}
	if skillDir != "" {
		return skillDir, nil
	}
	resolved, err := svc.ws.ResolveExistingContext(ctx, ".")
	if err != nil {
		return "", err
	}
	return resolved.Abs, nil
}

func (svc *Service) resolveSkillCommandDir(skill string) (string, error) {
	path, err := svc.resolveSkill(skill)
	if err != nil {
		return "", toolErrorDetails(
			"SKILL_CONTEXT_INVALID",
			"resolve active Skill directory: "+err.Error(),
			"validation",
			map[string]any{"skill": skill, "reason": err.Error()},
		)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", toolErrorDetails(
			"SKILL_CONTEXT_INVALID",
			"inspect active Skill directory: "+err.Error(),
			"validation",
			map[string]any{"skill": skill, "reason": err.Error()},
		)
	}
	if !info.IsDir() {
		return "", toolErrorDetails(
			"SKILL_CONTEXT_INVALID",
			"active Skill path is not a directory",
			"validation",
			map[string]any{"skill": skill, "path": path},
		)
	}
	return path, nil
}

func (svc *Service) commandEnvOverrides(skillName string, extra map[string]string) (map[string]string, error) {
	overrides := map[string]string{}
	if skillName != "" {
		values, err := svc.envs.Load(envstore.Scope{Kind: envstore.ScopeSkill, Name: skillName})
		if err != nil {
			return nil, toolErrorDetails("SKILL_ENV_INVALID", "load Skill environment", "validation", map[string]any{"skill": skillName, "reason": err.Error()})
		}
		for key, value := range values {
			setPlatformCommandEnv(overrides, key, value)
		}
	}
	for key, value := range extra {
		if err := envstore.ValidateKey(key); err != nil {
			return nil, toolErrorDetails("INVALID_ENV_NAME", err.Error(), "validation", map[string]any{"key": key})
		}
		setPlatformCommandEnv(overrides, key, value)
	}
	return overrides, nil
}
