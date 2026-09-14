package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLinuxInstallerSelectsExistingInstallationUser(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux installer shell behavior requires bash")
	}
	for _, test := range []struct {
		name           string
		currentUser    string
		sudoUser       string
		configuredUser string
		wantInstaller  string
		wantService    string
	}{
		{name: "ordinary user", currentUser: "installer", wantInstaller: "installer", wantService: "installer"},
		{name: "sudo uses original user", currentUser: "root", sudoUser: "installer", wantInstaller: "installer", wantService: "installer"},
		{name: "direct root installation", currentUser: "root", wantInstaller: "root", wantService: "root"},
		{name: "explicit service user", currentUser: "root", sudoUser: "installer", configuredUser: "existing-operator", wantInstaller: "installer", wantService: "existing-operator"},
	} {
		t.Run(test.name, func(t *testing.T) {
			script := `
set -Eeuo pipefail
id() {
  [[ "$*" == "-un" ]] || return 1
  printf '%s\n' "$TEST_CURRENT_USER"
}
source ../install/install-linux-platform.sh
printf 'INSTALLER=%s\nSERVICE=%s\n' "$INSTALLER_USER" "$DEFAULT_SERVICE_USER"
`
			cmd := exec.Command("bash", "-c", script)
			cmd.Env = append(os.Environ(),
				"AGENTDOCK_NONINTERACTIVE=true",
				"SUDO_USER="+test.sudoUser,
				"AGENTDOCK_SERVICE_USER="+test.configuredUser,
				"TEST_CURRENT_USER="+test.currentUser,
			)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("resolve installer identity: %v\n%s", err, output)
			}
			for _, want := range []string{"INSTALLER=" + test.wantInstaller, "SERVICE=" + test.wantService} {
				if !strings.Contains("\n"+string(output), "\n"+want+"\n") {
					t.Fatalf("installer identity missing %q:\n%s", want, output)
				}
			}
		})
	}
}

func TestLinuxInstallerDefaultsDataToSelectedUserHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux installer shell behavior requires bash")
	}
	for _, test := range []struct {
		name           string
		currentUser    string
		sudoUser       string
		configuredUser string
		selectedUser   string
		accountHome    string
		configuredData string
		wantData       string
	}{
		{name: "ordinary user", currentUser: "installer", selectedUser: "installer", accountHome: "/home/installer", wantData: "/home/installer"},
		{name: "sudo user home", currentUser: "root", sudoUser: "installer", selectedUser: "installer", accountHome: "/home/installer", wantData: "/home/installer"},
		{name: "root home", currentUser: "root", selectedUser: "root", accountHome: "/root", wantData: "/root"},
		{name: "explicit user home", currentUser: "installer", configuredUser: "existing-operator", selectedUser: "existing-operator", accountHome: "/home/operator", wantData: "/home/operator"},
		{name: "explicit data directory", currentUser: "installer", selectedUser: "installer", accountHome: "/home/installer", configuredData: "/mnt/agentdock-data", wantData: "/mnt/agentdock-data"},
	} {
		t.Run(test.name, func(t *testing.T) {
			capturedDefault := filepath.Join(t.TempDir(), "data-directory-default")
			script := `
set -Eeuo pipefail
id() {
  if [[ "$*" == "-un" ]]; then
    printf '%s\n' "$TEST_CURRENT_USER"
  elif [[ "$*" == "-gn $TEST_SELECTED_USER" ]]; then
    printf 'shared-developers\n'
  else
    [[ "$*" == "$TEST_SELECTED_USER" ]]
  fi
}
getent() {
  [[ "$1" == passwd && "$2" == "$TEST_SELECTED_USER" ]] || return 1
  printf '%s:x:1000:1001::%s:/bin/bash\n' "$TEST_SELECTED_USER" "$TEST_ACCOUNT_HOME"
}
source ../install/install-linux-platform.sh
require_linux() { :; }
repo_root_from_script() { :; }
detect_service_manager() { printf 'none'; }
run_root() { return 99; }
prompt() {
  if [[ "$1" == '运行数据根目录' ]]; then
    printf '%s' "$2" >"$TEST_CAPTURED_DEFAULT"
    return 42
  fi
  printf '%s' "${2:-}"
}
main
`
			cmd := exec.Command("bash", "-c", script)
			cmd.Env = append(os.Environ(),
				"AGENTDOCK_NONINTERACTIVE=true",
				"AGENTDOCK_INSTALL_MODE=binary",
				"SUDO_USER="+test.sudoUser,
				"AGENTDOCK_SERVICE_USER="+test.configuredUser,
				"AGENTDOCK_DATA_DIR="+test.configuredData,
				"HOME=/unrelated-inherited-home",
				"TEST_CURRENT_USER="+test.currentUser,
				"TEST_SELECTED_USER="+test.selectedUser,
				"TEST_ACCOUNT_HOME="+test.accountHome,
				"TEST_CAPTURED_DEFAULT="+capturedDefault,
			)
			output, err := cmd.CombinedOutput()
			if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 42 {
				t.Fatalf("installer did not reach data directory selection: %v\n%s", err, output)
			}
			data, err := os.ReadFile(capturedDefault)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != test.wantData {
				t.Fatalf("data directory default = %q, want %q", data, test.wantData)
			}
		})
	}
}

func TestLinuxInstallerRequiresUserWithoutCreatingAccounts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux installer shell behavior requires bash")
	}
	for _, user := range []string{"installer", "missing-user"} {
		t.Run(user, func(t *testing.T) {
			creationLog := filepath.Join(t.TempDir(), "account-creation.log")
			script := `
set -Eeuo pipefail
id() {
  case "$*" in
    -un) printf 'installer\n' ;;
    installer) return 0 ;;
    *) return 1 ;;
  esac
}
source ../install/install-linux-platform.sh
record_account_creation() {
  printf '%s\n' "$*" >>"$TEST_CREATION_LOG"
}
run_root() { record_account_creation "$@"; }
useradd() { record_account_creation useradd "$@"; }
adduser() { record_account_creation adduser "$@"; }
groupadd() { record_account_creation groupadd "$@"; }
addgroup() { record_account_creation addgroup "$@"; }
require_service_user "$TEST_SELECTED_USER"
printf 'USER_ACCEPTED\n'
`
			cmd := exec.Command("bash", "-c", script)
			cmd.Env = append(os.Environ(),
				"AGENTDOCK_NONINTERACTIVE=true",
				"SUDO_USER=",
				"AGENTDOCK_SERVICE_USER=",
				"TEST_SELECTED_USER="+user,
				"TEST_CREATION_LOG="+creationLog,
			)
			output, err := cmd.CombinedOutput()
			if user == "installer" {
				if err != nil || !strings.Contains(string(output), "USER_ACCEPTED") {
					t.Fatalf("existing user rejected: %v\n%s", err, output)
				}
			} else {
				if err == nil || strings.Contains(string(output), "USER_ACCEPTED") {
					t.Fatalf("missing user must stop installation:\n%s", output)
				}
				if !strings.Contains(string(output), "运行用户不存在：missing-user") {
					t.Fatalf("missing user diagnostic not reported:\n%s", output)
				}
			}
			if _, err := os.Stat(creationLog); !os.IsNotExist(err) {
				t.Fatalf("user validation attempted privileged account creation: %v", err)
			}
		})
	}
}

func TestLinuxInstallerPreparesOnlyManagedDataDirectoryPermissions(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux install, chown, and stat behavior")
	}
	for _, existingHome := range []bool{false, true} {
		name := "new nested data root"
		if existingHome {
			name = "existing private home"
		}
		t.Run(name, func(t *testing.T) {
			tempDir := t.TempDir()
			parent := filepath.Join(tempDir, "parent")
			if err := os.Mkdir(parent, 0o750); err != nil {
				t.Fatal(err)
			}
			dataDir := filepath.Join(parent, "new", "nested", "data")
			if existingHome {
				dataDir = filepath.Join(parent, "home")
				if err := os.Mkdir(dataDir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dataDir, "personal.txt"), []byte("private personal file\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			operationLog := filepath.Join(tempDir, "operations.log")
			script := `
set -Eeuo pipefail
source ../install/install-linux-platform.sh
umask 077
parent_before="$(stat -c '%u:%g:%a' "$TEST_PARENT")"
if [[ -d "$TEST_DATA_DIR" ]]; then
  home_before="$(stat -c '%u:%g:%a' "$TEST_DATA_DIR")"
  personal_before="$(stat -c '%u:%g:%a' "$TEST_DATA_DIR/personal.txt")"
fi
run_root() {
  printf '%s\t' "$@" >>"$TEST_OPERATION_LOG"
  printf '\n' >>"$TEST_OPERATION_LOG"
  "$@"
}
prepare_data_directories "$TEST_DATA_DIR" "$(id -un)" "$(id -gn)"
[[ "$(stat -c '%u:%g:%a' "$TEST_PARENT")" == "$parent_before" ]]
if [[ -n "${home_before:-}" ]]; then
  [[ "$(stat -c '%u:%g:%a' "$TEST_DATA_DIR")" == "$home_before" ]]
  [[ "$(stat -c '%u:%g:%a' "$TEST_DATA_DIR/personal.txt")" == "$personal_before" ]]
fi
for managed_dir in "$TEST_DATA_DIR/.agentdock" "$TEST_DATA_DIR/AgentDock"; do
  [[ "$(stat -c '%u:%g' "$managed_dir")" == "$(id -u):$(id -g)" ]]
done
`
			cmd := exec.Command("bash", "-c", script)
			cmd.Env = append(os.Environ(),
				"AGENTDOCK_NONINTERACTIVE=true",
				"SUDO_USER=",
				"AGENTDOCK_SERVICE_USER=",
				"TEST_PARENT="+parent,
				"TEST_DATA_DIR="+dataDir,
				"TEST_OPERATION_LOG="+operationLog,
			)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("prepare managed directory permissions: %v\n%s", err, output)
			}
			for _, managed := range []string{".agentdock", "AgentDock"} {
				info, err := os.Stat(filepath.Join(dataDir, managed))
				if err != nil || !info.IsDir() {
					t.Fatalf("managed directory %s was not created: %v", managed, err)
				}
			}
			if existingHome {
				data, err := os.ReadFile(filepath.Join(dataDir, "personal.txt"))
				if err != nil || string(data) != "private personal file\n" {
					t.Fatalf("personal file changed: %v, contents %q", err, data)
				}
				info, err := os.Stat(dataDir)
				if err != nil || info.Mode().Perm() != 0o700 {
					t.Fatalf("existing home must remain private: %v", err)
				}
			} else {
				for path := dataDir; path != parent; path = filepath.Dir(path) {
					info, err := os.Stat(path)
					if err != nil {
						t.Fatal(err)
					}
					if info.Mode().Perm()&0o111 != 0o111 {
						t.Fatalf("new data path %s is not traversable under umask 077: %o", path, info.Mode().Perm())
					}
				}
			}
			operations, err := os.ReadFile(operationLog)
			if err != nil {
				t.Fatal(err)
			}
			chownCount := 0
			for _, operation := range strings.Split(strings.TrimSpace(string(operations)), "\n") {
				args := strings.Split(strings.TrimSuffix(operation, "\t"), "\t")
				for _, arg := range args[1:] {
					if arg == parent || arg == filepath.Join(dataDir, "personal.txt") || (existingHome && arg == dataDir) {
						t.Fatalf("installer touched an unmanaged path: %s", operation)
					}
				}
				if args[0] == "chown" {
					chownCount++
					if len(args) != 5 || args[3] != filepath.Join(dataDir, ".agentdock") || args[4] != filepath.Join(dataDir, "AgentDock") {
						t.Fatalf("ownership change must be limited to managed directories: %s", operation)
					}
				}
			}
			if chownCount != 1 {
				t.Fatalf("got %d managed ownership changes, want 1", chownCount)
			}
		})
	}
}

func TestLinuxInstallerServicesUseSelectedUserAndPrimaryGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux installer shell behavior requires bash")
	}
	captureRoot := t.TempDir()
	script := `
set -Eeuo pipefail
id() {
  case "$*" in
    -un) printf 'installer\n' ;;
    selected-operator) return 0 ;;
    '-gn selected-operator') printf 'shared-developers\n' ;;
    *) return 1 ;;
  esac
}
source ../install/install-linux-platform.sh
run_root() {
  [[ "$1" == install ]] || return 1
  local source="${@: -2:1}" destination="${@: -1}"
  mkdir -p "$(dirname "$TEST_CAPTURE_ROOT$destination")"
  cp "$source" "$TEST_CAPTURE_ROOT$destination"
}
require_service_user "$DEFAULT_SERVICE_USER"
service_group="$(id -gn "$DEFAULT_SERVICE_USER")"
write_systemd_unit custom-core "$DEFAULT_SERVICE_USER" "$service_group" /opt/agentdock /etc/agentdock/agentdock.env
write_openrc_service custom-core "$DEFAULT_SERVICE_USER" "$service_group" /opt/agentdock /etc/agentdock/agentdock.env
write_cloudflared_systemd_unit custom-tunnel "$DEFAULT_SERVICE_USER" "$service_group" \
  /srv/agentdock /usr/local/bin/cloudflared /etc/agentdock/cloudflared.env quick \
  http://127.0.0.1:8765 /opt/agentdock/bin/agentdock /etc/agentdock
write_cloudflared_openrc_service custom-tunnel "$DEFAULT_SERVICE_USER" "$service_group" \
  /srv/agentdock /usr/local/bin/cloudflared /etc/agentdock/cloudflared.env quick \
  http://127.0.0.1:8765 /opt/agentdock/bin/agentdock /etc/agentdock
`
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = append(os.Environ(),
		"AGENTDOCK_NONINTERACTIVE=true",
		"SUDO_USER=",
		"AGENTDOCK_SERVICE_USER=selected-operator",
		"TEST_CAPTURE_ROOT="+captureRoot,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate service definitions: %v\n%s", err, output)
	}
	for _, test := range []struct {
		path  string
		lines []string
	}{
		{path: "etc/systemd/system/custom-core.service", lines: []string{"User=selected-operator", "Group=shared-developers"}},
		{path: "etc/systemd/system/custom-tunnel.service", lines: []string{"User=selected-operator", "Group=shared-developers"}},
		{path: "etc/init.d/custom-core", lines: []string{`command_user="selected-operator:shared-developers"`}},
		{path: "etc/init.d/custom-tunnel", lines: []string{`command_user="selected-operator:shared-developers"`}},
	} {
		t.Run(test.path, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(captureRoot, filepath.FromSlash(test.path)))
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range test.lines {
				if !strings.Contains("\n"+string(data), "\n"+line+"\n") {
					t.Fatalf("service definition missing %q:\n%s", line, data)
				}
			}
		})
	}
}

func TestInstallLinuxRemovesLegacyNexusCredentials(t *testing.T) {
	data, err := os.ReadFile("../install/install-linux-platform.sh")
	if err != nil {
		t.Fatalf("read install-linux-platform.sh: %v", err)
	}
	script := string(data)
	forbidden := []string{
		"local nexus_token=\"$7\"",
		"printf 'AGENTDOCK_NEXUS_TOKEN=%s\\n' \"$nexus_token\"",
		"NexusDock API 是否需要 token？",
		"nexus_token=\"$(prompt_secret 'NexusDock token')\"",
	}
	for _, value := range forbidden {
		if strings.Contains(script, value) {
			t.Fatalf("install-linux-platform.sh still contains legacy Nexus credential handling: %s", value)
		}
	}
	if !strings.Contains(script, "AGENTDOCK_NEXUS_ENDPOINT|AGENTDOCK_NEXUS_TOKEN") {
		t.Fatal("install-linux-platform.sh must remove legacy Nexus credentials from an existing env file")
	}
	for _, removed := range []string{"AGENTDOCK_NEXUS_DEVICE_NAME", "AGENTDOCK_NEXUS_HEARTBEAT_SECONDS", "Nexus 设备名"} {
		if strings.Contains(script, removed) {
			t.Fatalf("install-linux-platform.sh still contains removed device-agent config %q", removed)
		}
	}
}
func TestLinuxInstallerIntegratesCloudflareTunnelWithoutLeakingToken(t *testing.T) {
	data, err := os.ReadFile("../install/install-linux-platform.sh")
	if err != nil {
		t.Fatalf("read install-linux-platform.sh: %v", err)
	}
	script := string(data)
	for _, want := range []string{
		"你是否有已接入 Cloudflare 的域名？",
		"AGENTDOCK_CLOUDFLARE_TUNNEL_TOKEN",
		"AGENTDOCK_TUNNEL_MODE=$mode",
		"TUNNEL_TOKEN=$token",
		"EnvironmentFile=$cloudflared_env_file",
		"service launch-core --runtime-root $runtime_root",
		"tunnel launch --runtime-root $runtime_root",
		"write_runtime_manifest",
		`"service_manager": "$service_manager"`,
		"AGENTDOCK_SERVER_URL=%s\\n",
		"AGENTDOCK_OAUTH_ENABLED=%s\\n",
		"AGENTDOCK_OAUTH_PASSWORD=%s\\n",
		"AGENTDOCK_OAUTH_TOKEN_SECRET=%s\\n",
		"Bearer Token、OAuth 均已启用",
		`server_url="$TUNNEL_PUBLIC_URL"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("install-linux-platform.sh missing Cloudflare Tunnel integration %q", want)
		}
	}
	if strings.Contains(script, "--token $token") || strings.Contains(script, "--token \\$TUNNEL_TOKEN") {
		t.Fatal("cloudflared token must be provided through its private environment file, not process arguments")
	}
	for _, legacy := range []string{
		"ExecStart=$cloudflared_binary tunnel",
		`command="$cloudflared_binary"`,
		"ExecStart=$source_dir/bin/agentdock \\",
	} {
		if strings.Contains(script, legacy) {
			t.Fatalf("installer still emits legacy direct runtime command %q", legacy)
		}
	}
}
func TestLinuxInstallerPreservesCredentialsAndCapturesQuickURL(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux installer shell behavior is covered by the Alpine native runtime E2E")
	}
	tempDir := t.TempDir()
	envFile := filepath.Join(tempDir, "agentdock.env")
	initial := strings.Join([]string{
		"AGENTDOCK_HOST=127.0.0.9",
		"AGENTDOCK_PORT=19999",
		"AGENTDOCK_AUTH_TOKEN=stable-token",
		"AGENTDOCK_OAUTH_ENABLED=true",
		"AGENTDOCK_OAUTH_PASSWORD=stable-oauth-password",
		"AGENTDOCK_OAUTH_TOKEN_SECRET=stable-oauth-secret-0123456789abcdef",
		"AGENTDOCK_BROWSER_CDP_URL=http://127.0.0.1:9222",
		"HOME=/srv/old-agentdock",
		"export AGENTDOCK_HOME=/srv/old-agentdock/.agentdock",
		"AGENTDOCK_DEFAULT_DIR=/srv/old-agentdock/AgentDock",
		"",
	}, "\n")
	if err := os.WriteFile(envFile, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}

	script := `
set -Eeuo pipefail
source ../install/install-linux-platform.sh
run_root() {
  if [[ "$1" == systemctl ]]; then
    return 0
  fi
  if [[ "$1" == install ]]; then
    shift
    local args=()
    while (( $# > 0 )); do
      case "$1" in
        -o|-g) shift 2 ;;
        *) args+=("$1"); shift ;;
      esac
    done
    command install "${args[@]}"
    return
  fi
  "$@"
}
write_env_file "$TEST_ENV_FILE" 127.0.0.1 8765 stable-token info \
  https://new.trycloudflare.com yes true stable-oauth-password stable-oauth-secret-0123456789abcdef /srv/test-agentdock /home/installer
cloudflared_service_active() { return 0; }
cloudflared_quick_url() { printf 'https://new.trycloudflare.com'; }
start_cloudflared_service systemd agentdock-cloudflared quick ""
printf '\nCAPTURED=%s\n' "$TUNNEL_PUBLIC_URL"
`
	cmd := exec.Command("bash", "-c", script)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "TEST_ENV_FILE="+envFile)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run installer functions: %v\n%s", err, output)
	}
	got, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	for _, want := range []string{
		"HOME=/home/installer",
		"AGENTDOCK_HOME=/srv/test-agentdock/.agentdock",
		"AGENTDOCK_DEFAULT_DIR=/srv/test-agentdock/AgentDock",
		"AGENTDOCK_BROWSER_CDP_URL=http://127.0.0.1:9222",
		"AGENTDOCK_AUTH_TOKEN=stable-token",
		"AGENTDOCK_OAUTH_ENABLED=true",
		"AGENTDOCK_OAUTH_PASSWORD=stable-oauth-password",
		"AGENTDOCK_OAUTH_TOKEN_SECRET=stable-oauth-secret-0123456789abcdef",
		"AGENTDOCK_SERVER_URL=https://new.trycloudflare.com",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("rewritten env missing %q:\n%s", want, text)
		}
	}
	if !strings.Contains(string(output), "CAPTURED=https://new.trycloudflare.com") {
		t.Fatalf("quick URL was not captured: %s", output)
	}
	if strings.Contains(text, "/srv/old-agentdock") {
		t.Fatalf("rewritten env retained old runtime paths:\n%s", text)
	}
}
