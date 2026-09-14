package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/uvwt/agentdock/internal/computer"
	"github.com/uvwt/agentdock/internal/fs/securepath"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

// Cocoa requires its application loop on the process initial thread.
func init() { runtime.LockOSThread() }
func main() {
	if err := computer.RunMain(run); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	home, err := computer.HelperHome()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(home, 0700); err != nil {
		return err
	}
	if err = securepath.EnsurePrivate(home); err != nil {
		return err
	}
	if len(os.Args) != 2 {
		return fmt.Errorf("usage: agentdock-computer serve|enable|stop|disable|permissions|diagnose")
	}
	// Local commands are deliberately outside the model-facing RPC interface.
	switch os.Args[1] {
	case "enable":
		if err = os.WriteFile(filepath.Join(home, "ENABLED"), []byte("local user opt-in\n"), 0600); err != nil {
			return err
		}
		err = os.Remove(filepath.Join(home, "STOP"))
		if os.IsNotExist(err) {
			return nil
		}
		return err
	case "stop":
		return os.WriteFile(filepath.Join(home, "STOP"), []byte("local stop\n"), 0600)
	case "disable":
		if err = os.WriteFile(filepath.Join(home, "STOP"), []byte("local disable\n"), 0600); err != nil {
			return err
		}
		err = os.Remove(filepath.Join(home, "ENABLED"))
		if os.IsNotExist(err) {
			return nil
		}
		return err
	case "diagnose":
		backend, err := computer.NewNativeBackend()
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		status, err := backend.Status(ctx)
		if err != nil {
			return err
		}
		if err = json.NewEncoder(os.Stdout).Encode(status); err != nil {
			return err
		}
		if len(status.Displays) == 0 {
			return fmt.Errorf("no interactive display available")
		}
		shot, err := backend.Capture(ctx, status.Displays[0].DisplayID, "", 1600)
		if err != nil {
			return err
		}
		path := filepath.Join(home, "diagnostic.png")
		if err = os.WriteFile(path, shot.PNG, 0600); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"capture": "passed", "width": shot.Width, "height": shot.Height, "sha256": fmt.Sprintf("%x", sha256.Sum256(shot.PNG)), "local_image": path})
	case "permissions":
		computer.RequestNativePermissions()
		return nil
	case "serve":
	default:
		return fmt.Errorf("unknown helper command")
	}
	unlock, err := computer.LockDesktop(home)
	if err != nil {
		return fmt.Errorf("computer desktop is already owned by another helper: %w", err)
	}
	defer unlock()
	backend, err := computer.NewNativeBackend()
	if err != nil {
		return err
	}
	_, err = os.Stat(filepath.Join(home, "ENABLED"))
	engine, err := computer.NewEngine(home, backend, err == nil)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() { <-ctx.Done(); engine.Close(); os.Stdin.Close() }()
	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, err := os.Stat(filepath.Join(home, "ENABLED"))
				engine.SetEnabled(err == nil)
			}
		}
	}()
	return computer.Run(ctx, os.Stdin, os.Stdout, engine)
}
