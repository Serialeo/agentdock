//go:build darwin || linux

package filelock

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestKernelLockHelperProcess(t *testing.T) {
	path := os.Getenv("AGENTDOCK_TEST_KERNEL_LOCK")
	if path == "" {
		return
	}
	release, err := Acquire(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	fmt.Println("lock acquired")
	time.Sleep(time.Hour)
}

func TestKernelLockRecoversAfterOwnerIsKilled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.lock")
	cmd := exec.Command(os.Args[0], "-test.run=^TestKernelLockHelperProcess$")
	cmd.Env = append(os.Environ(), "AGENTDOCK_TEST_KERNEL_LOCK="+path)
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	ready := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		ready <- scanner.Scan() && scanner.Text() == "lock acquired"
	}()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("child did not acquire lock")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("child did not become ready")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if release, err := Acquire(ctx, path); err == nil {
		release()
		t.Fatal("stole a live child process lock")
	} else if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	// SIGKILL 不执行 release，磁盘标记必须仍在；恢复必须依赖内核锁。
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("owner marker missing after kill: %v", err)
	}
	ctx, cancel = context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	release, err := Acquire(ctx, path)
	if err != nil {
		t.Fatalf("acquire after SIGKILL: %v", err)
	}
	release()
	release() // Releasing twice must not close a subsequent holder's descriptor.
	if _, err := os.Stat(path + ".flock"); err != nil {
		t.Fatalf("persistent flock inode missing: %v", err)
	}
}

func TestKernelLockPreservesLegacyPIDOwners(t *testing.T) {
	for _, pid := range []int{os.Getpid(), 1 << 30} {
		t.Run(strconv.Itoa(pid), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.lock")
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
			ownerPath := filepath.Join(path, ownerPrefix+strings.Repeat("a", 32))
			data := strconv.Itoa(pid) + "\n"
			if err := os.WriteFile(ownerPath, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			if release, err := Acquire(ctx, path); err == nil {
				release()
				t.Fatal("legacy PID owner cannot be proven dead across PID namespaces")
			} else if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(ownerPath); err != nil || string(got) != data {
				t.Fatalf("legacy owner changed: %q, %v", got, err)
			}
		})
	}
}

func TestLegacyCallerCannotStealKernelOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.lock")
	release, err := Acquire(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if oldRelease, err := acquireDirectoryLock(ctx, path, false); err == nil {
		oldRelease()
		t.Fatal("legacy caller stole the kernel owner's marker")
	} else if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func TestKernelLockRecoversInterruptedMarkerCreation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.lock")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(path, kernelOwnerPrefix+strings.Repeat("b", 32))
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	release, err := Acquire(ctx, path)
	if err != nil {
		t.Fatalf("acquire after interrupted marker initialization: %v", err)
	}
	defer release()
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("interrupted owner was not replaced: %v", err)
	}
}
