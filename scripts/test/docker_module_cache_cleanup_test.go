package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestBrowserDockerCleanupRemovesReadOnlyModuleCache(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires a non-root POSIX user to reproduce read-only directory cleanup")
	}
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	_, browserStage, found := strings.Cut(string(data), "FROM browser AS browser-test")
	if !found {
		t.Fatal("browser-test stage not found")
	}
	match := regexp.MustCompile(`(?m)^\s*trap '([^']+)' EXIT;`).FindStringSubmatch(browserStage)
	if len(match) != 2 {
		t.Fatal("browser-test cleanup trap not found")
	}
	for _, exitCode := range []int{0, 23} {
		t.Run("exit_"+strconv.Itoa(exitCode), func(t *testing.T) {
			root := t.TempDir()
			home := filepath.Join(root, "private-home")
			modCache := filepath.Join(home, "go", "pkg", "mod")
			moduleDir := filepath.Join(modCache, "example.com", "fixture@v1.0.0")
			if err := os.MkdirAll(moduleDir, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(moduleDir, "go.mod"), []byte("module example.com/fixture\n"), 0444); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(moduleDir, 0555); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(moduleDir, 0755) })

			// Establish that the old rm-only cleanup really fails for this user.
			if out, err := exec.Command("rm", "-rf", home).CombinedOutput(); err == nil {
				t.Fatalf("read-only fixture unexpectedly removable: %s", out)
			}
			if err := os.WriteFile(filepath.Join(home, ".netrc"), []byte("fixture credential\n"), 0600); err != nil {
				t.Fatal(err)
			}
			// Execute the actual Dockerfile trap, without credentials or network.
			script := "set -eu; private_home=\"$1\"; trap '" + match[1] + "' EXIT; exit \"$2\""
			cmd := exec.Command(shell, "-c", script, "cleanup-test", home, strconv.Itoa(exitCode))
			cmd.Dir = root
			cmd.Env = append(os.Environ(),
				"HOME="+home, "GOPATH="+filepath.Join(home, "go"),
				"GOMODCACHE="+modCache, "GOTOOLCHAIN=local", "GOWORK=off")
			out, err := cmd.CombinedOutput()
			gotCode := 0
			if err != nil {
				exitErr, ok := err.(*exec.ExitError)
				if !ok {
					t.Fatalf("execute cleanup: %v\n%s", err, out)
				}
				gotCode = exitErr.ExitCode()
			}
			if gotCode != exitCode {
				t.Fatalf("cleanup changed exit status: got %d, want %d\n%s", gotCode, exitCode, out)
			}
			if _, err := os.Stat(home); !os.IsNotExist(err) {
				t.Fatalf("private home and module cache were not removed: %v\n%s", err, out)
			}
		})
	}
}
