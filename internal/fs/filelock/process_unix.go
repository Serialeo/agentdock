//go:build darwin || linux

package filelock

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"syscall"
	"time"
)

func acquirePlatform(ctx context.Context, path string) (func(), error) {
	// 文件保持在原 inode 上；删除它会让等待者和新调用者锁住不同文件。
	// os.OpenFile 设置 close-on-exec，启动的工具进程不能延长 Core 的锁寿命。
	file, err := os.OpenFile(path+".flock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open kernel file lock %s: %w", path, err)
	}
	fail := func(err error) (func(), error) {
		_ = file.Close()
		return nil, fmt.Errorf("acquire kernel file lock %s: %w", path, err)
	}
	info, err := file.Stat()
	if err != nil {
		return fail(err)
	}
	if !info.Mode().IsRegular() {
		return fail(errors.New("lock must be a regular file"))
	}
	ticker := time.NewTicker(pollDelay)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			// 文件系统不支持 flock 时明确失败，不能回退到有 PID 复用漏洞的锁。
			return fail(err)
		}
		select {
		case <-ctx.Done():
			return fail(ctx.Err())
		case <-ticker.C:
		}
	}
	releaseDirectory, err := acquireDirectoryLock(ctx, path, true)
	if err != nil {
		return fail(err)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			// 先清理兼容旧版的占用标记，再关闭 fd 释放内核锁。
			releaseDirectory()
			if err := file.Close(); err != nil {
				slog.Warn("close kernel file lock failed", "path", path, "error", err)
			}
		})
	}, nil
}

func openOwnerFile(path string) (*os.File, error) { return os.Open(path) }

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	if err == nil || errors.Is(err, syscall.EPERM) {
		return true
	}
	return !errors.Is(err, syscall.ESRCH)
}

func retryableLockCreationError(err error) bool {
	return errors.Is(err, os.ErrExist)
}
