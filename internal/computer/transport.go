package computer

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	protocol "github.com/Serialeo/agentdock-protocol"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

const maxFrame = 40 << 20

// HelperHome is fixed per OS user, independent of Core's configurable HOME.
// Two Core installations therefore cannot acquire separate locks on one desktop.
func HelperHome() (string, error) {
	home, err := platformConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "AgentDock", "computer"), nil
}
func Supported() bool { return runtime.GOOS == "windows" || runtime.GOOS == "darwin" }
func BundledHelper() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	name := "agentdock-computer"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(filepath.Dir(exe), "AgentDockComputer.app", "Contents", "MacOS", name)
	}
	return filepath.Join(filepath.Dir(exe), name)
}

// Run serves only inherited anonymous pipes: there is no local listening socket,
// bearer secret on disk, network port, shell command or model-selected executable.
func Run(ctx context.Context, in io.Reader, out io.Writer, e *Engine) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer e.Close()
	var outputMu sync.Mutex
	var requestsMu sync.Mutex
	pending := map[string]context.CancelFunc{}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				e.Maintain()
			}
		}
	}()
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4096), maxFrame)
	for scanner.Scan() {
		var req Request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			return err
		}
		if req.CancelID != "" {
			requestsMu.Lock()
			if stop := pending[req.CancelID]; stop != nil {
				stop()
			}
			requestsMu.Unlock()
			continue
		}
		callCtx, stop := context.WithCancel(ctx)
		requestsMu.Lock()
		if _, exists := pending[req.ID]; exists {
			requestsMu.Unlock()
			stop()
			return errors.New("duplicate IPC request id")
		}
		pending[req.ID] = stop
		requestsMu.Unlock()
		go func(req Request) {
			result, png, err := e.Call(callCtx, req)
			reply := Reply{ID: req.ID, Result: result, PNG: png}
			if err != nil {
				reply.Error = remoteError(err)
			}
			requestsMu.Lock()
			delete(pending, req.ID)
			requestsMu.Unlock()
			stop()
			outputMu.Lock()
			writeErr := json.NewEncoder(out).Encode(reply)
			outputMu.Unlock()
			if writeErr != nil {
				cancel()
				e.Close()
			}
		}(req)
	}
	return scanner.Err()
}

type Client struct {
	mu      sync.Mutex
	path    string
	process *connection
	closed  bool
}
type connection struct {
	cmd     *exec.Cmd
	in      io.WriteCloser
	mu      sync.Mutex
	writeMu sync.Mutex
	pending map[string]chan Reply
	done    chan struct{}
	err     error
}

func Start(ctx context.Context, path string) (*Client, error) {
	c := &Client{path: path}
	reply, err := c.Call(ctx, Request{Tool: "hello", Args: raw(map[string]any{})})
	if err != nil {
		c.Close()
		return nil, err
	}
	var hello struct {
		Version int `json:"wire_version"`
	}
	if json.Unmarshal(reply.Result, &hello) != nil || hello.Version != protocol.ComputerWireVersion {
		c.Close()
		return nil, failure(protocol.ErrorComputerContractMismatch, "helper IPC version does not match this Core; install the same release")
	}
	return c, nil
}
func (c *Client) connect() (*connection, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errors.New("computer client closed")
	}
	if c.process != nil {
		select {
		case <-c.process.done:
			c.process = nil
		default:
			return c.process, nil
		}
	}
	cmd := exec.Command(c.path, "serve")
	cmd.Stderr = os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		in.Close()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		in.Close()
		out.Close()
		return nil, err
	}
	p := &connection{cmd: cmd, in: in, pending: map[string]chan Reply{}, done: make(chan struct{})}
	c.process = p
	go func() {
		scanner := bufio.NewScanner(out)
		scanner.Buffer(make([]byte, 4096), maxFrame)
		for scanner.Scan() {
			var reply Reply
			if err := json.Unmarshal(scanner.Bytes(), &reply); err != nil {
				break
			}
			p.mu.Lock()
			ch := p.pending[reply.ID]
			delete(p.pending, reply.ID)
			p.mu.Unlock()
			if ch != nil {
				ch <- reply
			}
		}
		in.Close()
		_ = cmd.Process.Kill()
		err := cmd.Wait()
		if err == nil {
			err = io.EOF
		}
		p.mu.Lock()
		p.err = err
		p.mu.Unlock()
		close(p.done)
	}()
	return p, nil
}
func (c *Client) Call(ctx context.Context, req Request) (Reply, error) {
	p, err := c.connect()
	if err != nil {
		return Reply{}, failure(protocol.ErrorComputerHelperOffline, err.Error())
	}
	req.ID = id()
	ch := make(chan Reply, 1)
	p.mu.Lock()
	p.pending[req.ID] = ch
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.pending, req.ID); p.mu.Unlock() }()
	// 写入本身也受取消约束：helper 不读 stdin 时，不能让 stop 卡在管道锁后。
	written := make(chan error, 1)
	go func() { p.writeMu.Lock(); defer p.writeMu.Unlock(); written <- json.NewEncoder(p.in).Encode(req) }()
	select {
	case err = <-written:
		if err != nil {
			return Reply{}, err
		}
	case <-ctx.Done():
		_ = p.cmd.Process.Kill()
		return Reply{}, ctx.Err()
	case <-p.done:
		return Reply{}, failure(protocol.ErrorComputerHelperOffline, "helper exited")
	}
	select {
	case reply := <-ch:
		if reply.Error != nil {
			return reply, reply.Error
		}
		return reply, nil
	case <-ctx.Done():
		// 不重试输入。终止 helper 会撤销全部租约，下一次显式 status 可以读取持久化结果。
		_ = p.cmd.Process.Kill()
		return Reply{}, ctx.Err()
	case <-p.done:
		return Reply{}, failure(protocol.ErrorComputerExecutionUnknown, "helper exited; query operation_id before any new input")
	}
}
func (c *Client) Revoke(owner Owner) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := c.Call(ctx, Request{Tool: "revoke", Owner: owner}); err != nil {
		_ = c.Close()
	}
}
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	if c.process == nil {
		return nil
	}
	c.process.in.Close()
	select {
	case <-c.process.done:
		return nil
	case <-time.After(time.Second):
		return c.process.cmd.Process.Kill()
	}
}
