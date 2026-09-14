package client

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/uvwt/agentdock/internal/envstore"
)

type Manager struct {
	registryMu contextMutex
	closed     atomic.Bool
	mu         sync.RWMutex
	store      *store
	retired    sync.WaitGroup
	retireMu   sync.Mutex
	retireErr  error
	closeDone  chan struct{}
	closeErr   error
	envs       *envstore.Store
	servers    map[string]ServerConfig
	states     map[string]*serverState
}

type serverState struct {
	mu            contextMutex
	toolsLoaded   bool
	client        protocolClient
	tools         map[string]Tool
	lastError     string
	lastErrorCode string
	refreshedAt   time.Time
}

func NewManager(agentDockHome string, provided ...*envstore.Store) (*Manager, error) {
	registry := newStore(agentDockHome)
	servers, err := registry.load()
	if err != nil {
		return nil, err
	}
	envs := (*envstore.Store)(nil)
	if len(provided) > 0 {
		envs = provided[0]
	}
	if envs == nil {
		envs, err = envstore.New(agentDockHome)
		if err != nil {
			return nil, err
		}
	}
	states := make(map[string]*serverState, len(servers))
	for name := range servers {
		states[name] = &serverState{}
	}
	return &Manager{store: registry, envs: envs, servers: servers, states: states, closeDone: make(chan struct{})}, nil
}

func (m *Manager) Add(cfg ServerConfig) (ServerSummary, error) {
	cfg = normalizeServerConfig(cfg)
	if err := validateServerConfig(cfg); err != nil {
		return ServerSummary{}, newError("MCP_CONFIG_INVALID", err.Error(), false, map[string]any{"server": cfg.Name}, err)
	}
	m.registryMu.Lock()
	unlockRegistry := sync.OnceFunc(m.registryMu.Unlock)
	defer unlockRegistry()
	if err := m.ensureOpenLocked(); err != nil {
		return ServerSummary{}, err
	}
	servers, err := m.store.update(func(servers map[string]ServerConfig) error {
		if _, exists := servers[cfg.Name]; exists {
			return newError("MCP_SERVER_EXISTS", "dynamic MCP server already exists", false, map[string]any{"server": cfg.Name}, nil)
		}
		servers[cfg.Name] = cfg
		return nil
	})
	if err != nil {
		var mcpErr *Error
		if errors.As(err, &mcpErr) {
			return ServerSummary{}, err
		}
		return ServerSummary{}, newError("MCP_REGISTRY_WRITE_FAILED", "persist dynamic MCP server", false, map[string]any{"server": cfg.Name}, err)
	}

	m.mu.Lock()
	staleStates := m.replaceRegistryLocked(servers)
	state := m.states[cfg.Name]
	m.mu.Unlock()
	waitRetired := m.retireStates(staleStates)
	unlockRegistry()
	if err := waitRetired(); err != nil {
		return ServerSummary{}, err
	}
	return summaryFor(cfg, state), nil
}

func (m *Manager) Remove(name string) error {
	name = strings.TrimSpace(name)
	m.registryMu.Lock()
	unlockRegistry := sync.OnceFunc(m.registryMu.Unlock)
	defer unlockRegistry()
	if err := m.ensureOpenLocked(); err != nil {
		return err
	}
	servers, err := m.store.update(func(servers map[string]ServerConfig) error {
		if _, exists := servers[name]; !exists {
			return newError("MCP_SERVER_NOT_FOUND", "dynamic MCP server not found", false, map[string]any{"server": name}, nil)
		}
		delete(servers, name)
		return nil
	})
	if err != nil {
		var mcpErr *Error
		if errors.As(err, &mcpErr) {
			return err
		}
		return newError("MCP_REGISTRY_WRITE_FAILED", "remove dynamic MCP server", false, map[string]any{"server": name}, err)
	}

	m.mu.Lock()
	staleStates := m.replaceRegistryLocked(servers)
	m.mu.Unlock()
	waitRetired := m.retireStates(staleStates)
	unlockRegistry()
	return waitRetired()
}

func (m *Manager) SetEnabled(name string, enabled bool) (ServerSummary, error) {
	name = strings.TrimSpace(name)
	m.registryMu.Lock()
	unlockRegistry := sync.OnceFunc(m.registryMu.Unlock)
	defer unlockRegistry()
	if err := m.ensureOpenLocked(); err != nil {
		return ServerSummary{}, err
	}
	var selected ServerConfig
	servers, err := m.store.update(func(servers map[string]ServerConfig) error {
		cfg, exists := servers[name]
		if !exists {
			return newError("MCP_SERVER_NOT_FOUND", "dynamic MCP server not found", false, map[string]any{"server": name}, nil)
		}
		cfg.Enabled = enabled
		servers[name] = cfg
		selected = cfg
		return nil
	})
	if err != nil {
		var mcpErr *Error
		if errors.As(err, &mcpErr) {
			return ServerSummary{}, err
		}
		return ServerSummary{}, newError("MCP_REGISTRY_WRITE_FAILED", "persist dynamic MCP server state", false, map[string]any{"server": name}, err)
	}

	m.mu.Lock()
	staleStates := m.replaceRegistryLocked(servers)
	state := m.states[name]
	m.mu.Unlock()
	waitRetired := m.retireStates(staleStates)
	unlockRegistry()
	if err := waitRetired(); err != nil {
		return ServerSummary{}, err
	}
	if !enabled {
		if err := closeState(state); err != nil {
			return ServerSummary{}, err
		}
	}
	return summaryFor(selected, state), nil
}

func (m *Manager) replaceRegistryLocked(servers map[string]ServerConfig) []*serverState {
	states := make(map[string]*serverState, len(servers))
	stale := make([]*serverState, 0)
	for name, cfg := range servers {
		if previous, exists := m.servers[name]; exists && reflect.DeepEqual(previous, cfg) {
			states[name] = m.states[name]
			continue
		}
		if previousState := m.states[name]; previousState != nil {
			stale = append(stale, previousState)
		}
		states[name] = &serverState{}
	}
	for name, state := range m.states {
		if _, exists := servers[name]; !exists && state != nil {
			stale = append(stale, state)
		}
	}
	m.servers = servers
	m.states = states
	return stale
}

func closeServerStates(states []*serverState) error {
	var result error
	for _, state := range states {
		result = errors.Join(result, closeState(state))
	}
	return result
}

func (m *Manager) syncRegistry() error {
	return m.syncRegistryContext(context.Background())
}

func (m *Manager) syncRegistryContext(ctx context.Context) error {
	if err := m.registryMu.LockContext(ctx); err != nil {
		return queueError(err)
	}
	unlockRegistry := sync.OnceFunc(m.registryMu.Unlock)
	defer unlockRegistry()
	if err := m.ensureOpenLocked(); err != nil {
		return err
	}
	servers, err := m.store.loadContext(ctx)
	if err != nil {
		return newError("MCP_REGISTRY_READ_FAILED", "read dynamic MCP registry", true, nil, err)
	}
	m.mu.Lock()
	staleStates := m.replaceRegistryLocked(servers)
	m.mu.Unlock()
	m.retireStates(staleStates)
	unlockRegistry()
	return nil
}

func (m *Manager) List() []ServerSummary {
	if err := m.syncRegistry(); err != nil {
		slog.Warn("refresh dynamic MCP registry before list failed", "error", err)
	}
	m.mu.RLock()
	names := make([]string, 0, len(m.servers))
	for name := range m.servers {
		names = append(names, name)
	}
	sort.Strings(names)
	configs := make([]ServerConfig, 0, len(names))
	states := make([]*serverState, 0, len(names))
	for _, name := range names {
		configs = append(configs, m.servers[name])
		states = append(states, m.states[name])
	}
	m.mu.RUnlock()
	items := make([]ServerSummary, 0, len(names))
	for i, cfg := range configs {
		items = append(items, summaryFor(cfg, states[i]))
	}
	return items
}

func (m *Manager) EnabledIndex() []ServerSummary {
	all := m.List()
	items := make([]ServerSummary, 0, len(all))
	for _, item := range all {
		if item.Enabled {
			items = append(items, item)
		}
	}
	return items
}

func (m *Manager) Inspect(name string) (ServerConfig, ServerSummary, error) {
	if err := m.syncRegistry(); err != nil {
		return ServerConfig{}, ServerSummary{}, err
	}
	m.mu.RLock()
	cfg, exists := m.servers[strings.TrimSpace(name)]
	state := m.states[strings.TrimSpace(name)]
	m.mu.RUnlock()
	if !exists {
		return ServerConfig{}, ServerSummary{}, newError("MCP_SERVER_NOT_FOUND", "dynamic MCP server not found", false, map[string]any{"server": name}, nil)
	}
	return cfg, summaryFor(cfg, state), nil
}

func (m *Manager) Refresh(ctx context.Context, name string) (ServerSummary, []ToolSummary, error) {
	started := time.Now()
	ctx, cancelRequest := m.requestContext(ctx, strings.TrimSpace(name), started)
	defer cancelRequest()
	if err := m.syncRegistryContext(ctx); err != nil {
		return ServerSummary{}, nil, err
	}
	ctx, cfg, state, unlockState, err := m.lockServerContext(ctx, strings.TrimSpace(name), started)
	if err != nil {
		return ServerSummary{}, nil, err
	}
	defer unlockState()
	if !cfg.Enabled {
		return ServerSummary{}, nil, newError("MCP_SERVER_DISABLED", "dynamic MCP server is disabled", false, map[string]any{"server": cfg.Name}, nil)
	}
	runtimeCfg, err := m.runtimeConfig(cfg)
	if err != nil {
		recordStateError(state, err)
		return ServerSummary{}, nil, err
	}
	tools, err := refreshStateLocked(ctx, runtimeCfg, state)
	summary := summaryForLocked(cfg, state)
	if err != nil {
		return summary, nil, err
	}
	return summary, summarizeTools(cfg.Name, tools), nil
}

func (m *Manager) Search(ctx context.Context, query, server string, limit int) ([]ToolSummary, error) {
	if err := m.syncRegistryContext(ctx); err != nil {
		return nil, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	server = strings.TrimSpace(server)
	if query == "" {
		return nil, newError("MCP_QUERY_REQUIRED", "MCP tool search query is required", false, nil, nil)
	}
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}

	configs, err := m.searchServers(server)
	if err != nil {
		return nil, err
	}
	type scoredTool struct {
		score int
		item  ToolSummary
	}
	matches := make([]scoredTool, 0)
	var firstErr error
	for _, cfg := range configs {
		tools, ensureErr := m.ensureTools(ctx, cfg.Name)
		if ensureErr != nil {
			if server != "" {
				return nil, ensureErr
			}
			if firstErr == nil {
				firstErr = ensureErr
			}
			continue
		}
		for _, tool := range tools {
			score := toolMatchScore(query, tool)
			if score == 0 {
				continue
			}
			matches = append(matches, scoredTool{score: score, item: toolSummary(cfg.Name, tool)})
		}
	}
	if len(matches) == 0 && firstErr != nil {
		return nil, firstErr
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].item.QualifiedName < matches[j].item.QualifiedName
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	items := make([]ToolSummary, 0, len(matches))
	for _, match := range matches {
		items = append(items, match.item)
	}
	return items, nil
}

func (m *Manager) InspectTool(ctx context.Context, qualifiedName string) (string, Tool, error) {
	if err := m.syncRegistryContext(ctx); err != nil {
		return "", Tool{}, err
	}
	server, name, err := splitQualifiedToolName(qualifiedName)
	if err != nil {
		return "", Tool{}, newError("MCP_TOOL_NAME_INVALID", err.Error(), false, map[string]any{"tool": qualifiedName}, err)
	}
	tools, err := m.ensureTools(ctx, server)
	if err != nil {
		return "", Tool{}, err
	}
	tool, exists := tools[name]
	if !exists {
		return "", Tool{}, newError("MCP_TOOL_NOT_FOUND", "MCP tool not found", false, map[string]any{"tool": qualifiedName}, nil)
	}
	return server, tool, nil
}

func (m *Manager) Call(ctx context.Context, qualifiedName string, arguments map[string]any) (map[string]any, error) {
	started := time.Now()
	server, name, err := splitQualifiedToolName(qualifiedName)
	if err != nil {
		return nil, newError("MCP_TOOL_NAME_INVALID", err.Error(), false, map[string]any{"tool": qualifiedName}, err)
	}
	ctx, cancelRequest := m.requestContext(ctx, server, started)
	defer cancelRequest()
	if err := m.syncRegistryContext(ctx); err != nil {
		return nil, err
	}
	ctx, cfg, state, unlockState, err := m.lockServerContext(ctx, server, started)
	if err != nil {
		return nil, err
	}
	defer unlockState()
	if !cfg.Enabled {
		return nil, newError("MCP_SERVER_DISABLED", "dynamic MCP server is disabled", false, map[string]any{"server": server}, nil)
	}
	if state.client == nil || connectionClosed(state.client) || !state.toolsLoaded {
		runtimeCfg, err := m.runtimeConfig(cfg)
		if err != nil {
			recordStateError(state, err)
			return nil, err
		}
		if _, err := refreshStateLocked(ctx, runtimeCfg, state); err != nil {
			return nil, err
		}
	}
	tool, exists := state.tools[name]
	if !exists {
		return nil, newError("MCP_TOOL_NOT_FOUND", "MCP tool not found", false, map[string]any{"tool": qualifiedName}, nil)
	}
	if err := validateToolArguments(tool, arguments); err != nil {
		return nil, err
	}
	result, err := state.client.callTool(ctx, name, arguments)
	if err != nil {
		var callErr *Error
		if errors.As(err, &callErr) && callErr.Code == "MCP_CONNECTION_FAILED" {
			// 断线后只在下一次显式请求重建，绝不重放可能已生效的写操作。
			_ = state.client.close()
			state.client = nil
			state.tools = nil
			state.toolsLoaded = false
			err = newError("MCP_EXECUTION_UNKNOWN", "MCP connection lost; tool execution result is unknown", false, map[string]any{"server": server, "tool": name, "execution_status": "unknown"}, err)
			recordStateError(state, err)
		}
		return nil, err
	}
	return result, nil
}

func (m *Manager) Close() error {
	m.registryMu.Lock()
	if m.closed.Swap(true) {
		m.registryMu.Unlock()
		<-m.closeDone
		return m.closeErr
	}
	m.mu.RLock()
	states := make([]*serverState, 0, len(m.states))
	for _, state := range m.states {
		states = append(states, state)
	}
	m.mu.RUnlock()
	m.registryMu.Unlock()
	m.closeErr = closeServerStates(states)
	m.retired.Wait()
	m.closeErr = errors.Join(m.closeErr, m.retireErr)
	close(m.closeDone)
	return m.closeErr
}

// 注册表锁内登记回收，锁外等待；Close 的完成屏障包含已被替换的连接。
func (m *Manager) retireStates(states []*serverState) func() error {
	if len(states) == 0 {
		return func() error { return nil }
	}
	done := make(chan struct{})
	var result error
	m.retired.Add(1)
	go func() {
		defer m.retired.Done()
		result = closeServerStates(states)
		if result != nil {
			m.retireMu.Lock()
			m.retireErr = errors.Join(m.retireErr, result)
			m.retireMu.Unlock()
			slog.Warn("close retired MCP connections failed", "error", result)
		}
		close(done)
	}()
	return func() error { <-done; return result }
}

// contextMutex keeps queueing cancellable without spawning a waiter goroutine.
type contextMutex struct {
	once  sync.Once
	token chan struct{}
}

func (m *contextMutex) LockContext(ctx context.Context) error {
	m.once.Do(func() { m.token = make(chan struct{}, 1) })
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case m.token <- struct{}{}:
		if err := ctx.Err(); err != nil {
			m.Unlock()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (m *contextMutex) Lock()   { _ = m.LockContext(context.Background()) }
func (m *contextMutex) Unlock() { <-m.token }

func (m *Manager) requestContext(ctx context.Context, name string, started time.Time) (context.Context, context.CancelFunc) {
	m.mu.RLock()
	cfg, exists := m.servers[name]
	m.mu.RUnlock()
	timeout := maxTimeoutMS
	if exists {
		timeout = cfg.TimeoutMS
	}
	return context.WithDeadline(ctx, started.Add(time.Duration(timeout)*time.Millisecond))
}

func queueError(err error) error {
	return newError("MCP_TIMEOUT", "MCP request canceled or timed out while queueing", true, map[string]any{"phase": "queue_wait"}, err)
}

func (m *Manager) lockServer(name string) (ServerConfig, *serverState, func(), error) {
	_, cfg, state, unlock, err := m.lockServerContext(context.Background(), name, time.Now())
	return cfg, state, unlock, err
}

func (m *Manager) lockServerContext(ctx context.Context, name string, started time.Time) (context.Context, ServerConfig, *serverState, func(), error) {
	if m.closed.Load() {
		return ctx, ServerConfig{}, nil, nil, newError("MCP_MANAGER_CLOSED", "dynamic MCP manager is closed", false, nil, nil)
	}
	m.mu.RLock()
	cfg, exists := m.servers[name]
	state := m.states[name]
	m.mu.RUnlock()
	if !exists {
		return ctx, ServerConfig{}, nil, nil, newError("MCP_SERVER_NOT_FOUND", "dynamic MCP server not found", false, map[string]any{"server": name}, nil)
	}
	ctx, cancel := context.WithDeadline(ctx, started.Add(time.Duration(cfg.TimeoutMS)*time.Millisecond))
	if err := state.mu.LockContext(ctx); err != nil {
		cancel()
		return ctx, ServerConfig{}, nil, nil, queueError(err)
	}
	unlock := func() { state.mu.Unlock(); cancel() }
	if m.closed.Load() {
		unlock()
		return ctx, ServerConfig{}, nil, nil, newError("MCP_MANAGER_CLOSED", "dynamic MCP manager is closed", false, nil, nil)
	}
	// 状态指针就是配置代次；等待期间被移除或替换的配置不得继续执行。
	m.mu.RLock()
	current := m.states[name] == state
	m.mu.RUnlock()
	if !current {
		unlock()
		return ctx, ServerConfig{}, nil, nil, newError("MCP_CONFIG_CHANGED", "MCP server configuration changed while queueing", true, map[string]any{"server": name}, nil)
	}
	return ctx, cfg, state, unlock, nil
}

func (m *Manager) ensureOpenLocked() error {
	if !m.closed.Load() {
		return nil
	}
	return newError("MCP_MANAGER_CLOSED", "dynamic MCP manager is closed", false, nil, nil)
}

func (m *Manager) runtimeConfig(cfg ServerConfig) (ServerConfig, error) {
	values, err := m.envs.Load(envstore.Scope{Kind: envstore.ScopeMCP, Name: cfg.Name})
	if err != nil {
		return ServerConfig{}, newError(
			"MCP_ENV_READ_FAILED",
			"read dynamic MCP environment",
			false,
			map[string]any{"server": cfg.Name},
			err,
		)
	}
	cfg.RuntimeEnv = values
	return cfg, nil
}

func (m *Manager) searchServers(name string) ([]ServerConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if name != "" {
		cfg, exists := m.servers[name]
		if !exists {
			return nil, newError("MCP_SERVER_NOT_FOUND", "dynamic MCP server not found", false, map[string]any{"server": name}, nil)
		}
		if !cfg.Enabled {
			return nil, newError("MCP_SERVER_DISABLED", "dynamic MCP server is disabled", false, map[string]any{"server": name}, nil)
		}
		return []ServerConfig{cfg}, nil
	}
	configs := make([]ServerConfig, 0, len(m.servers))
	for _, cfg := range m.servers {
		if cfg.Enabled {
			configs = append(configs, cfg)
		}
	}
	sort.Slice(configs, func(i, j int) bool { return configs[i].Name < configs[j].Name })
	return configs, nil
}

func (m *Manager) ensureTools(ctx context.Context, name string) (map[string]Tool, error) {
	ctx, cfg, state, unlockState, err := m.lockServerContext(ctx, name, time.Now())
	if err != nil {
		return nil, err
	}
	defer unlockState()
	if !cfg.Enabled {
		return nil, newError("MCP_SERVER_DISABLED", "dynamic MCP server is disabled", false, map[string]any{"server": name}, nil)
	}
	if state.client == nil || connectionClosed(state.client) || !state.toolsLoaded {
		runtimeCfg, err := m.runtimeConfig(cfg)
		if err != nil {
			recordStateError(state, err)
			return nil, err
		}
		return refreshStateLocked(ctx, runtimeCfg, state)
	}
	return cloneTools(state.tools), nil
}

func refreshStateLocked(ctx context.Context, cfg ServerConfig, state *serverState) (map[string]Tool, error) {
	if state.client != nil {
		_ = state.client.close()
	}
	state.client = nil
	state.tools = nil
	state.toolsLoaded = false
	client, err := newProtocolClient(cfg)
	if err != nil {
		recordStateError(state, err)
		return nil, err
	}
	if err := client.initialize(ctx); err != nil {
		_ = client.close()
		recordStateError(state, err)
		return nil, err
	}
	listed, err := client.listTools(ctx)
	if err != nil {
		_ = client.close()
		recordStateError(state, err)
		return nil, err
	}
	tools := make(map[string]Tool, len(listed))
	for _, tool := range listed {
		tool.Name = strings.TrimSpace(tool.Name)
		if tool.Name == "" {
			_ = client.close()
			err := newError("MCP_INVALID_RESPONSE", "MCP tools/list returned an empty tool name", false, map[string]any{"server": cfg.Name}, nil)
			recordStateError(state, err)
			return nil, err
		}
		if _, duplicate := tools[tool.Name]; duplicate {
			_ = client.close()
			err := newError("MCP_INVALID_RESPONSE", "MCP tools/list returned duplicate tool names", false, map[string]any{"server": cfg.Name, "tool": tool.Name}, nil)
			recordStateError(state, err)
			return nil, err
		}
		if tool.InputSchema == nil {
			tool.InputSchema = map[string]any{"type": "object", "additionalProperties": true}
		}
		validator, err := compileToolInputSchema(tool.InputSchema)
		if err != nil {
			_ = client.close()
			schemaErr := newError(
				"MCP_SCHEMA_INVALID",
				"MCP tools/list returned an invalid input schema",
				false,
				map[string]any{"server": cfg.Name, "tool": tool.Name, "reason": err.Error()},
				err,
			)
			recordStateError(state, schemaErr)
			return nil, schemaErr
		}
		tool.inputValidator = validator
		tools[tool.Name] = tool
	}
	state.client = client
	state.tools = tools
	state.toolsLoaded = true
	state.lastError = ""
	state.lastErrorCode = ""
	state.refreshedAt = time.Now().UTC()
	return cloneTools(tools), nil
}

func newProtocolClient(cfg ServerConfig) (protocolClient, error) {
	switch cfg.Transport {
	case TransportStreamableHTTP:
		return newStreamableHTTPClient(cfg), nil
	case TransportStdio:
		return newStdioClient(cfg), nil
	default:
		return nil, newError("MCP_TRANSPORT_UNSUPPORTED", fmt.Sprintf("unsupported MCP transport %q", cfg.Transport), false, map[string]any{"server": cfg.Name}, nil)
	}
}

func closeState(state *serverState) error {
	if state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	var err error
	if state.client != nil {
		err = state.client.close()
	}
	state.client = nil
	state.tools = nil
	state.toolsLoaded = false
	state.lastError = ""
	state.lastErrorCode = ""
	state.refreshedAt = time.Time{}
	return err
}

func summaryFor(cfg ServerConfig, state *serverState) ServerSummary {
	state.mu.Lock()
	defer state.mu.Unlock()
	return summaryForLocked(cfg, state)
}

func summaryForLocked(cfg ServerConfig, state *serverState) ServerSummary {
	status := "idle"
	if !cfg.Enabled {
		status = "disabled"
	} else if connectionClosed(state.client) {
		status = "error"
	} else if state.lastError != "" {
		status = "error"
	} else if state.client != nil {
		status = "ready"
	}
	item := ServerSummary{
		Name:          cfg.Name,
		Description:   cfg.Description,
		Transport:     cfg.Transport,
		Enabled:       cfg.Enabled,
		Status:        status,
		ToolCount:     len(state.tools),
		LastError:     state.lastError,
		LastErrorCode: state.lastErrorCode,
	}
	if connectionClosed(state.client) {
		item.LastError = "MCP connection is closed"
		item.LastErrorCode = "MCP_CONNECTION_FAILED"
	}
	if !state.refreshedAt.IsZero() {
		item.RefreshedAt = state.refreshedAt.Format(time.RFC3339Nano)
	}
	return item
}

func connectionClosed(client protocolClient) bool {
	c, ok := client.(interface{ isClosed() bool })
	return ok && c.isClosed()
}

func recordStateError(state *serverState, err error) {
	state.lastError = err.Error()
	state.lastErrorCode = "MCP_ERROR"
	var mcpErr *Error
	if errors.As(err, &mcpErr) {
		state.lastErrorCode = mcpErr.Code
	}
}

func summarizeTools(server string, tools map[string]Tool) []ToolSummary {
	items := make([]ToolSummary, 0, len(tools))
	for _, tool := range tools {
		items = append(items, toolSummary(server, tool))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].QualifiedName < items[j].QualifiedName })
	return items
}

func toolSummary(server string, tool Tool) ToolSummary {
	return ToolSummary{
		Name:          tool.Name,
		QualifiedName: qualifiedToolName(server, tool.Name),
		Title:         tool.Title,
		Description:   tool.Description,
		Server:        server,
	}
}

func cloneTools(input map[string]Tool) map[string]Tool {
	out := make(map[string]Tool, len(input))
	for name, tool := range input {
		out[name] = tool
	}
	return out
}

func toolMatchScore(query string, tool Tool) int {
	if query == "*" {
		return 1
	}
	name := strings.ToLower(tool.Name)
	title := strings.ToLower(tool.Title)
	description := strings.ToLower(tool.Description)
	score := 0
	if name == query {
		score += 100
	} else if strings.Contains(name, query) {
		score += 60
	}
	if strings.Contains(title, query) {
		score += 30
	}
	if strings.Contains(description, query) {
		score += 20
	}
	for _, token := range strings.Fields(query) {
		if strings.Contains(name, token) {
			score += 10
		}
		if strings.Contains(title, token) || strings.Contains(description, token) {
			score += 5
		}
	}
	return score
}
