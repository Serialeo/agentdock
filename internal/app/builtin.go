package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	protocol "github.com/Serialeo/agentdock-protocol"
	acpruntime "github.com/uvwt/agentdock/internal/acp"
	"github.com/uvwt/agentdock/internal/builtin"
	"github.com/uvwt/agentdock/internal/config"
	toolacp "github.com/uvwt/agentdock/internal/tool/acp"
	toolbrowser "github.com/uvwt/agentdock/internal/tool/browser"
)

type builtinGroup struct {
	state  protocol.BuiltinCapability
	ctx    context.Context
	cancel context.CancelFunc
	done   <-chan struct{}
	calls  sync.WaitGroup
}

// builtinManager 串行化配置写入和后端替换；mu 只保护快照与调用准入，不跨后端等待。
// 每次启用创建新的取消域，关闭后旧请求与旧任务不能进入下一代后端。
type builtinManager struct {
	updates      chan struct{}
	mu           sync.Mutex
	groups       map[string]*builtinGroup
	choices      builtin.Choices
	listeners    map[int]func()
	nextListener int
	closed       bool
}

func (r *Runtime) initBuiltins() error {
	choices, err := builtin.Load(r.cfg.AgentDockHome, r.cfg.Builtins)
	if err != nil {
		return err
	}
	r.builtins = &builtinManager{updates: make(chan struct{}, 1), choices: choices, groups: map[string]*builtinGroup{}, listeners: map[int]func(){}}
	r.builtins.updates <- struct{}{}
	for _, id := range []string{"browser", "acp"} {
		provided := config.BuiltinProvided(id)
		enabled := choices.Browser
		if id == "acp" {
			enabled = choices.ACP
		}
		state := protocol.BuiltinCapability{ID: id, Provided: provided, Enabled: enabled, Tools: []string{}}
		for _, spec := range toolSpecs {
			if spec.Group == id {
				state.Tools = append(state.Tools, spec.Name)
			}
		}
		g := &builtinGroup{state: state}
		r.builtins.groups[id] = g
		if provided && enabled {
			r.startBuiltin(g)
		}
	}
	for _, g := range r.builtins.groups {
		r.watchBuiltin(g)
	}
	return nil
}

func (r *Runtime) startBuiltin(g *builtinGroup) {
	g.ctx, g.cancel = context.WithCancel(context.Background())
	var err error
	var warning string
	switch g.state.ID {
	case "browser":
		service := toolbrowser.New(toolbrowser.Config{AgentDockHome: r.cfg.AgentDockHome, ExecutablePath: r.cfg.BrowserExecutablePath, CDPURL: r.cfg.BrowserCDPURL, ReuseExistingCDP: r.cfg.BrowserReuseExistingCDP}, r.media.PublishBrowserScreenshot)
		ctx, cancel := context.WithTimeout(g.ctx, 5*time.Second)
		defaultErr := service.CheckReady(ctx)
		cancel()
		// 服务可接收按调用指定的 CDP；默认后端故障不能撤下这条公开入口。
		r.browser = service
		if defaultErr != nil {
			warning = "默认浏览器后端不可用，可在启动会话时指定 cdp_url: " + defaultErr.Error()
		}
	case "acp":
		cfg := r.cfg
		err = cfg.ValidateACPBackend()
		if err != nil {
			break
		}
		environment := make(map[string]string, len(cfg.ACPEnvFromEnv))
		for child, host := range cfg.ACPEnvFromEnv {
			value, exists := os.LookupEnv(host)
			if !exists {
				err = fmt.Errorf("required ACP environment variable %s is missing", host)
				break
			}
			environment[child] = value
		}
		if err != nil {
			break
		}
		var manager *acpruntime.Manager
		manager, err = acpruntime.NewManager(acpruntime.Options{Home: cfg.AgentDockHome, DefaultCWD: cfg.AgentDockDefaultDir, Agent: acpruntime.AgentSpec{Name: cfg.ACPAgentName, Command: cfg.ACPCommand, Args: append([]string(nil), cfg.ACPArgs...), Environment: environment}, MaxConcurrentRuns: cfg.ACPMaxPrompts, InteractionTimeout: time.Duration(cfg.ACPInteractionMS) * time.Millisecond})
		if err == nil {
			// 握手只启动适配器，不恢复 session 或 prompt；失败只影响这一组。
			ctx, cancel := context.WithTimeout(g.ctx, 10*time.Second)
			_, err = manager.AgentInfo(ctx)
			cancel()
			if err != nil {
				err = errors.Join(err, manager.Close())
			} else {
				r.acp = toolacp.New(manager, r.ws)
				g.done = manager.BackendDone()
			}
		}
	}
	g.state.Ready = err == nil
	g.state.Reason = warning
	if err != nil {
		g.state.Reason = err.Error()
		g.cancel()
	}
}

func groupAvailable(g *builtinGroup) bool {
	return g != nil && g.state.Provided && g.state.Enabled && g.state.Ready && !g.state.Transitioning
}

func builtinState(g *builtinGroup) protocol.BuiltinCapability {
	state := g.state
	state.Tools = append([]string{}, state.Tools...)
	state.Available = groupAvailable(g)
	switch {
	case !state.Provided:
		state.Reason = "当前发行包不提供此能力"
	case state.Transitioning:
		state.Reason = "正在切换，已停止接受新调用"
	case !state.Enabled:
		if state.Reason == "" {
			state.Reason = "用户已关闭"
		}
	}
	return state
}

func (r *Runtime) BuiltinCapabilities() []protocol.BuiltinCapability {
	if r.builtins == nil {
		return nil
	}
	r.builtins.mu.Lock()
	defer r.builtins.mu.Unlock()
	states := make([]protocol.BuiltinCapability, 0, 2)
	for _, id := range []string{"browser", "acp"} {
		state := builtinState(r.builtins.groups[id])
		state.Available = state.Available && !r.builtins.closed
		states = append(states, state)
	}
	return states
}

func (r *Runtime) builtinAvailable(id string) bool {
	if id == "" {
		return true
	}
	if r.builtins == nil {
		return false
	}
	r.builtins.mu.Lock()
	defer r.builtins.mu.Unlock()
	g := r.builtins.groups[id]
	return g != nil && groupAvailable(g) && !r.builtins.closed
}

func (r *Runtime) enterBuiltin(ctx context.Context, id string) (context.Context, func(), error) {
	if id == "" {
		return ctx, func() {}, nil
	}
	m := r.builtins
	if m == nil {
		return nil, nil, toolError("CAPABILITY_UNAVAILABLE", "built-in capability is unavailable", "capability")
	}
	m.mu.Lock()
	g := m.groups[id]
	if g == nil || !groupAvailable(g) || m.closed {
		reason := "built-in capability is unavailable"
		if g != nil {
			reason = builtinState(g).Reason
		}
		m.mu.Unlock()
		return nil, nil, toolErrorDetails("CAPABILITY_UNAVAILABLE", reason, "capability", map[string]any{"group": id})
	}
	g.calls.Add(1)
	callCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(g.ctx, cancel)
	m.mu.Unlock()
	return callCtx, func() { stop(); cancel(); g.calls.Done() }, nil
}

// SubscribeToolsChanged applies to built-ins only; external MCP service configuration has its own owner.
func (r *Runtime) SubscribeToolsChanged(listener func()) func() {
	m := r.builtins
	m.mu.Lock()
	id := m.nextListener
	m.nextListener++
	m.listeners[id] = listener
	m.mu.Unlock()
	return func() { m.mu.Lock(); delete(m.listeners, id); m.mu.Unlock() }
}

func (r *Runtime) notifyBuiltinChange() {
	m := r.builtins
	m.mu.Lock()
	listeners := make([]func(), 0, len(m.listeners))
	for _, f := range m.listeners {
		listeners = append(listeners, f)
	}
	m.mu.Unlock()
	for _, f := range listeners {
		f()
	}
}

func (m *builtinManager) lockUpdates(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.updates:
		return nil
	}
}
func (m *builtinManager) unlockUpdates() { m.updates <- struct{}{} }

// 已持久化的操作负责完成清理；请求超时只结束等待，不能提前替换仍被旧调用使用的后端。
func (r *Runtime) SetBuiltin(ctx context.Context, update protocol.BuiltinUpdate) (Result, error) {
	m := r.builtins
	if err := m.lockUpdates(ctx); err != nil {
		return nil, err
	}
	type outcome struct {
		result Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		defer m.unlockUpdates()
		result, err := r.setBuiltin(ctx, update)
		done <- outcome{result, err}
	}()
	select {
	case value := <-done:
		return value.result, value.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *Runtime) setBuiltin(ctx context.Context, update protocol.BuiltinUpdate) (Result, error) {
	m := r.builtins
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	g := m.groups[update.ID]
	if g == nil || update.Enabled == nil {
		m.mu.Unlock()
		return nil, toolError("INVALID_ARGUMENT", "id and enabled are required for a known built-in group", "validation")
	}
	if m.closed {
		m.mu.Unlock()
		return nil, toolError("RUNTIME_CLOSING", "AgentDock runtime is shutting down", "runtime")
	}
	if *update.Enabled && !g.state.Provided {
		m.mu.Unlock()
		return nil, toolError("CAPABILITY_NOT_PROVIDED", "当前发行包不提供此能力", "capability")
	}
	if g.state.Enabled == *update.Enabled && ((*update.Enabled && g.state.Ready) || (!*update.Enabled && g.state.Reason == "")) && !g.state.Transitioning {
		m.mu.Unlock()
		return r.RuntimeBuiltins(), nil
	}
	choices := m.choices
	switch update.ID {
	case "browser":
		choices.Browser = *update.Enabled
	case "acp":
		choices.ACP = *update.Enabled
	}
	m.mu.Unlock()
	// 先完成持久化再改变准入；写入失败不能给 GUI 返回已生效的假象。
	if err := builtin.Save(r.cfg.AgentDockHome, choices); err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.choices = choices
	g.state.Enabled = *update.Enabled
	g.state.Transitioning = true
	g.state.Ready = false
	if g.cancel != nil {
		g.cancel()
	}
	m.mu.Unlock()
	r.notifyBuiltinChange()
	// Close 负责取消 detached ACP prompt 及 browser session；等待在途 handler 后才替换指针。
	closeErr := r.stopBuiltin(g, "capability_disabled")
	g.calls.Wait()
	m.mu.Lock()
	g.state.Reason = ""
	closing := m.closed
	m.mu.Unlock()
	if *update.Enabled && closeErr == nil && !closing {
		// startBuiltin 只写本组工作副本，快照读取在整个启动期间仍显示 transitioning。
		m.mu.Lock()
		next := &builtinGroup{state: g.state}
		m.mu.Unlock()
		r.startBuiltin(next)
		m.mu.Lock()
		g.done = next.done
		g.ctx = next.ctx
		g.cancel = next.cancel
		g.state = next.state
		m.mu.Unlock()
	}
	m.mu.Lock()
	g.state.Transitioning = false
	if closeErr != nil {
		g.state.Reason = fmt.Sprintf("后端清理失败: %v", closeErr)
	}
	if m.closed {
		g.state.Ready = false
		g.state.Reason = "AgentDock 正在关闭"
		if g.cancel != nil {
			g.cancel()
		}
	}
	m.mu.Unlock()
	r.notifyBuiltinChange()
	r.watchBuiltin(g)
	return r.RuntimeBuiltins(), nil
}

func (r *Runtime) stopBuiltin(g *builtinGroup, reason string) error {
	switch g.state.ID {
	case "browser":
		if r.browser != nil {
			return r.browser.Close()
		}
	case "acp":
		if r.acp != nil {
			return r.acp.CloseWithReason(reason)
		}
	}
	return nil
}

func (r *Runtime) closeBuiltins() error {
	if r.builtins == nil {
		return nil
	}
	m := r.builtins
	m.mu.Lock()
	m.closed = true
	for _, g := range m.groups {
		g.state.Ready = false
		g.state.Reason = "AgentDock 正在关闭"
		if g.cancel != nil {
			g.cancel()
		}
	}
	m.mu.Unlock()
	_ = m.lockUpdates(context.Background())
	defer m.unlockUpdates()
	var errs []error
	for _, g := range m.groups {
		errs = append(errs, r.stopBuiltin(g, "agentdock_shutdown"))
		g.calls.Wait()
	}
	return errors.Join(errs...)
}

func (r *Runtime) RuntimeBuiltins() Result {
	return Result{"ok": true, "builtins": r.BuiltinCapabilities(), "source": "agentdock"}
}

// CatalogSnapshot prevents a switch from splitting tool names, descriptors and capability states across generations.
func (r *Runtime) CatalogSnapshot() ([]ToolDefinition, []protocol.BuiltinCapability) {
	states := r.BuiltinCapabilities()
	allowed := make(map[string]bool, len(states))
	for _, state := range states {
		allowed[state.ID] = state.Available
	}
	definitions := make([]ToolDefinition, 0, len(r.toolNames))
	for _, name := range r.toolNames {
		spec, _ := toolSpecByName(name)
		if spec.Group != "" && !allowed[spec.Group] {
			continue
		}
		definitions = append(definitions, cloneToolDefinition(r.toolDefinitions[name]))
	}
	return definitions, states
}

func (r *Runtime) watchBuiltin(g *builtinGroup) {
	m := r.builtins
	m.mu.Lock()
	if m.closed || g.done == nil || !g.state.Ready {
		m.mu.Unlock()
		return
	}
	generation, done := g.ctx, g.done
	m.mu.Unlock()
	go func() {
		select {
		case <-generation.Done():
			return
		case <-done:
		}
		_ = m.lockUpdates(context.Background())
		defer m.unlockUpdates()
		m.mu.Lock()
		if m.closed || g.ctx != generation || generation.Err() != nil {
			m.mu.Unlock()
			return
		}
		g.state.Ready = false
		g.state.Reason = "ACP 适配器已断开，请检查后端后重新启用"
		g.cancel()
		m.mu.Unlock()
		r.notifyBuiltinChange()
		err := r.stopBuiltin(g, "backend_disconnected")
		g.calls.Wait()
		if err != nil {
			m.mu.Lock()
			g.state.Reason += ": " + err.Error()
			m.mu.Unlock()
			r.notifyBuiltinChange()
		}
	}()
}
