#!/bin/sh
set -eu

ROOT_DIR="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/agentdock-install-entry-test.XXXXXX")"
trap 'rm -rf "$TMP_ROOT"' EXIT HUP INT TERM

export AGENTDOCK_TTY_IN=/dev/stdin
export AGENTDOCK_TTY_OUT=/dev/stderr

RELEASE_DIR="$TMP_ROOT/release"
FAKE_BIN="$TMP_ROOT/bin"
ENTRY="$TMP_ROOT/install.sh"
mkdir -p "$RELEASE_DIR" "$FAKE_BIN"
cp "$ROOT_DIR/scripts/install/install.sh" "$ENTRY"
chmod +x "$ENTRY"

checksum_asset() {
  asset="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "$RELEASE_DIR" && sha256sum "$asset" >"$asset.sha256")
  elif command -v shasum >/dev/null 2>&1; then
    (cd "$RELEASE_DIR" && shasum -a 256 "$asset" >"$asset.sha256")
  else
    digest="$(openssl dgst -sha256 "$RELEASE_DIR/$asset" | awk '{print $NF}')"
    printf '%s  %s\n' "$digest" "$asset" >"$RELEASE_DIR/$asset.sha256"
  fi
}

assert_line() {
  expected="$1"
  file="$2"
  grep -Fqx "$expected" "$file"
}

assert_args() {
  output="$1"
  [ "$(sed -n '1p' "$output")" = "--mode" ]
  [ "$(sed -n '2p' "$output")" = "value with spaces" ]
}

cat >"$FAKE_BIN/uname" <<'SH'
#!/bin/sh
printf '%s\n' "${TEST_UNAME:?}"
SH
chmod +x "$FAKE_BIN/uname"

cat >"$FAKE_BIN/zsh" <<'SH'
#!/bin/sh
exec /bin/sh "$@"
SH
chmod +x "$FAKE_BIN/zsh"

cat >"$FAKE_BIN/systemctl" <<'SH'
#!/bin/sh
if [ -n "${TEST_SYSTEMCTL_LOG:-}" ]; then
  printf '%s\n' "$*" >>"$TEST_SYSTEMCTL_LOG"
fi
exit 0
SH
chmod +x "$FAKE_BIN/systemctl"

cat >"$FAKE_BIN/journalctl" <<'SH'
#!/bin/sh
if [ -n "${TEST_JOURNAL_CONTENT:-}" ]; then
  printf '%s\n' "$TEST_JOURNAL_CONTENT"
fi
exit 0
SH
chmod +x "$FAKE_BIN/journalctl"

cat >"$FAKE_BIN/sudo" <<'SH'
#!/bin/sh
if [ -n "${TEST_SUDO_LOG:-}" ]; then
  printf '%s\n' "$*" >>"$TEST_SUDO_LOG"
fi
exec "$@"
SH
chmod +x "$FAKE_BIN/sudo"

for command_name in rc-service rc-update; do
  cat >"$FAKE_BIN/$command_name" <<'SH'
#!/bin/sh
if [ -n "${TEST_OPENRC_LOG:-}" ]; then
  printf '%s %s\n' "$(basename "$0")" "$*" >>"$TEST_OPENRC_LOG"
fi
exit 0
SH
  chmod +x "$FAKE_BIN/$command_name"
done

write_fake_platform() {
  asset="$1"
  cat >"$RELEASE_DIR/$asset" <<'SH'
#!/bin/sh
set -eu

if [ -n "${TEST_OUTPUT:-}" ]; then
  printf '%s\n' "$@" >"$TEST_OUTPUT"
fi

existing_server_url=''
if [ -n "${AGENTDOCK_ENV_FILE:-}" ] && [ -f "$AGENTDOCK_ENV_FILE" ]; then
  # shellcheck disable=SC2016
  existing_server_url="$(awk -F= '$1 == "AGENTDOCK_SERVER_URL" {value=substr($0, index($0, "=") + 1)} END {print value}' "$AGENTDOCK_ENV_FILE")"
fi
if [ -n "${TEST_ENV_OUTPUT:-}" ]; then
  {
    printf 'noninteractive=%s\n' "${AGENTDOCK_NONINTERACTIVE:-}"
    printf 'tunnel_mode=%s\n' "${AGENTDOCK_TUNNEL_MODE:-}"
    printf 'server_url=%s\n' "${AGENTDOCK_SERVER_URL:-}"
    printf 'tunnel_token=%s\n' "${AGENTDOCK_CLOUDFLARE_TUNNEL_TOKEN:-}"
    printf 'existing_server_url=%s\n' "$existing_server_url"
  } >"$TEST_ENV_OUTPUT"
fi

if [ "${TEST_WRITE_ENV:-false}" = "true" ]; then
  : "${AGENTDOCK_ENV_FILE:?}"
  config_dir="$(dirname "$AGENTDOCK_ENV_FILE")"
  mkdir -p "$config_dir"
  public_url="${AGENTDOCK_SERVER_URL:-}"
  if [ "${AGENTDOCK_TUNNEL_MODE:-none}" = "quick" ]; then
    public_url="https://quick-test.trycloudflare.com"
  fi
  cat >"$AGENTDOCK_ENV_FILE" <<ENV
AGENTDOCK_HOST=127.0.0.1
AGENTDOCK_PORT=8765
AGENTDOCK_AUTH_TOKEN=test-bearer-token
AGENTDOCK_OAUTH_PASSWORD=test-oauth-password
AGENTDOCK_SERVER_URL=$public_url
ENV
  {
    printf 'AGENTDOCK_TUNNEL_MODE=%s\n' "${AGENTDOCK_TUNNEL_MODE:-none}"
    printf 'TUNNEL_TOKEN=%s\n' "${AGENTDOCK_CLOUDFLARE_TUNNEL_TOKEN:-}"
  } >"$config_dir/cloudflared.env"
fi

if [ "${TEST_FAIL_RATE_LIMIT:-false}" = "true" ]; then
  printf '%s\n' 'error code: 1015 status_code="429 Too Many Requests"' >&2
  exit 1
fi
if [ "${TEST_FAIL_GENERIC:-false}" = "true" ]; then
  printf '%s\n' 'simulated installer failure' >&2
  exit 2
fi
SH
  chmod +x "$RELEASE_DIR/$asset"
  checksum_asset "$asset"
}

write_fake_platform install-linux-platform.sh
cp "$ROOT_DIR/scripts/install/uninstall-linux.sh" "$RELEASE_DIR/uninstall-linux.sh"
chmod +x "$RELEASE_DIR/uninstall-linux.sh"
checksum_asset uninstall-linux.sh

# CI、容器和管道执行时 /dev/tty 可能存在但无法打开，必须自动回退到标准流。
if command -v setsid >/dev/null 2>&1; then
  NO_TTY_ROOT="$TMP_ROOT/no-tty"
  mkdir -p "$NO_TTY_ROOT/systemd" "$NO_TTY_ROOT/openrc"
  (
    unset AGENTDOCK_TTY_IN AGENTDOCK_TTY_OUT
    PATH="$FAKE_BIN:$PATH" \
      AGENTDOCK_NO_SUDO=true \
      AGENTDOCK_ENV_FILE="$NO_TTY_ROOT/config/agentdock.env" \
      AGENTDOCK_SOURCE_DIR="$NO_TTY_ROOT/source" \
      AGENTDOCK_DATA_DIR="$NO_TTY_ROOT/data" \
      AGENTDOCK_SYSTEMD_DIR="$NO_TTY_ROOT/systemd" \
      AGENTDOCK_OPENRC_DIR="$NO_TTY_ROOT/openrc" \
      setsid sh "$RELEASE_DIR/uninstall-linux.sh" --services-only </dev/null \
        >"$NO_TTY_ROOT/stdout.log" 2>"$NO_TTY_ROOT/stderr.log"
  )
  grep -Fq 'AgentDock 已卸载' "$NO_TTY_ROOT/stderr.log"
fi

# 安装器由其他 AgentDock 实例启动时，父进程的实例目录不能泄漏到新部署的 Skill bootstrap。
SERVICE_HOME_ROOT="$TMP_ROOT/service-user-home"
SERVICE_ENV_OUTPUT="$TMP_ROOT/service-user-env"
mkdir -p "$SERVICE_HOME_ROOT"
cat >"$FAKE_BIN/capture-service-env" <<'SH'
#!/bin/sh
set -eu
: "${TEST_SERVICE_ENV_OUTPUT:?}"
{
  printf 'HOME=%s\n' "${HOME:-}"
  printf 'AGENTDOCK_HOME=%s\n' "${AGENTDOCK_HOME:-}"
  printf 'AGENTDOCK_DEFAULT_DIR=%s\n' "${AGENTDOCK_DEFAULT_DIR:-}"
} >"$TEST_SERVICE_ENV_OUTPUT"
SH
chmod +x "$FAKE_BIN/capture-service-env"
TEST_SERVICE_ENV_OUTPUT="$SERVICE_ENV_OUTPUT" \
  AGENTDOCK_HOME="$TMP_ROOT/inherited/.agentdock" \
  AGENTDOCK_DEFAULT_DIR="$TMP_ROOT/inherited/AgentDock" \
  bash -c '
    set -Eeuo pipefail
    source "$1"
    run_as_service_user "$(id -un)" "$2" "$3"
  ' bash "$ROOT_DIR/scripts/install/install-linux-platform.sh" "$SERVICE_HOME_ROOT" "$FAKE_BIN/capture-service-env"
SERVICE_ACCOUNT_HOME="$(bash -c 'source "$1"; service_user_home "$(id -un)"' bash "$ROOT_DIR/scripts/install/install-linux-platform.sh")"
assert_line "HOME=$SERVICE_ACCOUNT_HOME" "$SERVICE_ENV_OUTPUT"
assert_line "AGENTDOCK_HOME=$SERVICE_HOME_ROOT/.agentdock" "$SERVICE_ENV_OUTPUT"
assert_line "AGENTDOCK_DEFAULT_DIR=$SERVICE_HOME_ROOT/AgentDock" "$SERVICE_ENV_OUTPUT"

# 首次安装默认只需选择公网模式，并完整保留平台参数。
LINUX_OUTPUT="$TMP_ROOT/linux.args"
LINUX_ENV_OUTPUT="$TMP_ROOT/linux.env"
printf '\n' | PATH="$FAKE_BIN:$PATH" \
  TEST_UNAME=Linux \
  TEST_OUTPUT="$LINUX_OUTPUT" \
  TEST_ENV_OUTPUT="$LINUX_ENV_OUTPUT" \
  AGENTDOCK_NO_SUDO=true \
  AGENTDOCK_ENV_FILE="$TMP_ROOT/linux-config/agentdock.env" \
  AGENTDOCK_INSTALLER_BASE_URL="file://$RELEASE_DIR" \
  sh "$ENTRY" --mode "value with spaces"
assert_args "$LINUX_OUTPUT"
assert_line 'noninteractive=true' "$LINUX_ENV_OUTPUT"
assert_line 'tunnel_mode=none' "$LINUX_ENV_OUTPUT"

# 本地开发模式必须从同一目录读取平台安装器和卸载器。
LOCAL_ROOT="$TMP_ROOT/local"
mkdir -p "$LOCAL_ROOT"
cp "$ENTRY" "$LOCAL_ROOT/install.sh"
cp "$RELEASE_DIR/install-linux-platform.sh" "$LOCAL_ROOT/install-linux-platform.sh"
cp "$RELEASE_DIR/uninstall-linux.sh" "$LOCAL_ROOT/uninstall-linux.sh"
printf '\n' | PATH="$FAKE_BIN:$PATH" \
  TEST_UNAME=Linux \
  TEST_ENV_OUTPUT="$LOCAL_ROOT/platform.env" \
  AGENTDOCK_NO_SUDO=true \
  AGENTDOCK_ENV_FILE="$LOCAL_ROOT/config/agentdock.env" \
  AGENTDOCK_USE_LOCAL_PLATFORM_INSTALLER=true \
  sh "$LOCAL_ROOT/install.sh"
assert_line 'noninteractive=true' "$LOCAL_ROOT/platform.env"
assert_line 'tunnel_mode=none' "$LOCAL_ROOT/platform.env"

# 显式非交互模式不能进入菜单，参数仍需透传。
NONINTERACTIVE_ROOT="$TMP_ROOT/noninteractive"
mkdir -p "$NONINTERACTIVE_ROOT"
PATH="$FAKE_BIN:$PATH" \
  TEST_UNAME=Linux \
  TEST_OUTPUT="$NONINTERACTIVE_ROOT/args" \
  TEST_ENV_OUTPUT="$NONINTERACTIVE_ROOT/env" \
  AGENTDOCK_NO_SUDO=true \
  AGENTDOCK_NONINTERACTIVE=true \
  AGENTDOCK_TUNNEL_MODE=none \
  AGENTDOCK_ENV_FILE="$NONINTERACTIVE_ROOT/config/agentdock.env" \
  AGENTDOCK_INSTALLER_BASE_URL="file://$RELEASE_DIR" \
  sh "$ENTRY" --mode "value with spaces"
assert_args "$NONINTERACTIVE_ROOT/args"
assert_line 'noninteractive=true' "$NONINTERACTIVE_ROOT/env"

# 普通用户通过 sudo 读取已有配置时，仍应识别当前 Tunnel 模式。
SUDO_ROOT="$TMP_ROOT/sudo-read"
mkdir -p "$SUDO_ROOT/config" "$SUDO_ROOT/systemd"
printf 'AGENTDOCK_HOST=127.0.0.1\n' >"$SUDO_ROOT/config/agentdock.env"
printf 'AGENTDOCK_TUNNEL_MODE=quick\n' >"$SUDO_ROOT/config/cloudflared.env"
printf '\n' | PATH="$FAKE_BIN:$PATH" \
  TEST_UNAME=Linux \
  TEST_ENV_OUTPUT="$SUDO_ROOT/platform.env" \
  TEST_SUDO_LOG="$SUDO_ROOT/sudo.log" \
  AGENTDOCK_SERVICE_MANAGER=systemd \
  AGENTDOCK_ENV_FILE="$SUDO_ROOT/config/agentdock.env" \
  AGENTDOCK_SYSTEMD_DIR="$SUDO_ROOT/systemd" \
  AGENTDOCK_INSTALLER_BASE_URL="file://$RELEASE_DIR" \
  sh "$ENTRY"
assert_line 'noninteractive=true' "$SUDO_ROOT/platform.env"
if [ "$(id -u)" -ne 0 ]; then
  grep -Fq "test -f $SUDO_ROOT/config/agentdock.env" "$SUDO_ROOT/sudo.log"
  grep -Fq 'awk -F= -v key=AGENTDOCK_TUNNEL_MODE' "$SUDO_ROOT/sudo.log"
  grep -Fq "$SUDO_ROOT/config/cloudflared.env" "$SUDO_ROOT/sudo.log"
fi

# Quick Tunnel 在 systemd 下安装有限重试 drop-in。
QUICK_ROOT="$TMP_ROOT/quick"
QUICK_SYSTEMD="$QUICK_ROOT/systemd"
mkdir -p "$QUICK_SYSTEMD"
printf '2\n' | PATH="$FAKE_BIN:$PATH" \
  TEST_UNAME=Linux \
  TEST_ENV_OUTPUT="$QUICK_ROOT/platform.env" \
  TEST_WRITE_ENV=true \
  TEST_SYSTEMCTL_LOG="$QUICK_ROOT/systemctl.log" \
  AGENTDOCK_NO_SUDO=true \
  AGENTDOCK_SERVICE_MANAGER=systemd \
  AGENTDOCK_ENV_FILE="$QUICK_ROOT/config/agentdock.env" \
  AGENTDOCK_SYSTEMD_DIR="$QUICK_SYSTEMD" \
  AGENTDOCK_INSTALLER_BASE_URL="file://$RELEASE_DIR" \
  sh "$ENTRY"
assert_line 'tunnel_mode=quick' "$QUICK_ROOT/platform.env"
QUICK_GUARD="$QUICK_SYSTEMD/agentdock-cloudflared.service.d/retry-limit.conf"
[ -f "$QUICK_GUARD" ]
grep -Fq 'StartLimitBurst=3' "$QUICK_GUARD"
grep -Fq 'RestartSec=30' "$QUICK_GUARD"

# 二次运行默认更新/修复，并沿用已有 Quick Tunnel 模式。
UPDATE_ROOT="$TMP_ROOT/update"
mkdir -p "$UPDATE_ROOT/config" "$UPDATE_ROOT/systemd"
printf '%s\n' \
  'AGENTDOCK_HOST=127.0.0.1' \
  'AGENTDOCK_PORT=8765' \
  'AGENTDOCK_AUTH_TOKEN=preserved-token' \
  >"$UPDATE_ROOT/config/agentdock.env"
printf 'AGENTDOCK_TUNNEL_MODE=quick\nTUNNEL_TOKEN=\n' >"$UPDATE_ROOT/config/cloudflared.env"
printf '\n' | PATH="$FAKE_BIN:$PATH" \
  TEST_UNAME=Linux \
  TEST_ENV_OUTPUT="$UPDATE_ROOT/platform.env" \
  AGENTDOCK_NO_SUDO=true \
  AGENTDOCK_SERVICE_MANAGER=systemd \
  AGENTDOCK_ENV_FILE="$UPDATE_ROOT/config/agentdock.env" \
  AGENTDOCK_SYSTEMD_DIR="$UPDATE_ROOT/systemd" \
  AGENTDOCK_INSTALLER_BASE_URL="file://$RELEASE_DIR" \
  sh "$ENTRY"
assert_line 'noninteractive=true' "$UPDATE_ROOT/platform.env"
assert_line 'tunnel_mode=' "$UPDATE_ROOT/platform.env"
[ -f "$UPDATE_ROOT/systemd/agentdock-cloudflared.service.d/retry-limit.conf" ]

# none、quick、named 可重复切换；Named 必须接受新地址和新 Token。
echo 'AGENTDOCK_SERVER_URL=https://old.example.com' >>"$UPDATE_ROOT/config/agentdock.env"
mkdir -p "$UPDATE_ROOT/systemd-named"
printf '2\n3\nhttps://new.example.com\nnew-tunnel-token\n' | PATH="$FAKE_BIN:$PATH" \
  TEST_UNAME=Linux \
  TEST_ENV_OUTPUT="$UPDATE_ROOT/reconfigure.env" \
  TEST_WRITE_ENV=true \
  AGENTDOCK_NO_SUDO=true \
  AGENTDOCK_SERVICE_MANAGER=systemd \
  AGENTDOCK_ENV_FILE="$UPDATE_ROOT/config/agentdock.env" \
  AGENTDOCK_SYSTEMD_DIR="$UPDATE_ROOT/systemd-named" \
  AGENTDOCK_INSTALLER_BASE_URL="file://$RELEASE_DIR" \
  sh "$ENTRY"
assert_line 'tunnel_mode=named' "$UPDATE_ROOT/reconfigure.env"
assert_line 'server_url=https://new.example.com' "$UPDATE_ROOT/reconfigure.env"
assert_line 'tunnel_token=new-tunnel-token' "$UPDATE_ROOT/reconfigure.env"
assert_line 'TUNNEL_TOKEN=new-tunnel-token' "$UPDATE_ROOT/config/cloudflared.env"

# Named Token 替换失败时，同时恢复旧 URL、模式和 Token。
ROLLBACK_ROOT="$TMP_ROOT/named-rollback"
mkdir -p "$ROLLBACK_ROOT/config" "$ROLLBACK_ROOT/systemd"
printf 'AGENTDOCK_SERVER_URL=https://stable.example.com\nCUSTOM_SETTING=keep\n' >"$ROLLBACK_ROOT/config/agentdock.env"
printf 'AGENTDOCK_TUNNEL_MODE=named\nTUNNEL_TOKEN=stable-token\n' >"$ROLLBACK_ROOT/config/cloudflared.env"
cp "$ROLLBACK_ROOT/config/agentdock.env" "$ROLLBACK_ROOT/agentdock.expected"
cp "$ROLLBACK_ROOT/config/cloudflared.env" "$ROLLBACK_ROOT/cloudflared.expected"
if printf '2\n3\nhttps://broken.example.com\nbroken-token\n' | PATH="$FAKE_BIN:$PATH" \
  TEST_UNAME=Linux \
  TEST_WRITE_ENV=true \
  TEST_FAIL_GENERIC=true \
  TEST_SYSTEMCTL_LOG="$ROLLBACK_ROOT/systemctl.log" \
  AGENTDOCK_NO_SUDO=true \
  AGENTDOCK_SERVICE_MANAGER=systemd \
  AGENTDOCK_ENV_FILE="$ROLLBACK_ROOT/config/agentdock.env" \
  AGENTDOCK_SYSTEMD_DIR="$ROLLBACK_ROOT/systemd" \
  AGENTDOCK_INSTALLER_BASE_URL="file://$RELEASE_DIR" \
  sh "$ENTRY" >"$ROLLBACK_ROOT/stdout.log" 2>"$ROLLBACK_ROOT/stderr.log"; then
  printf 'failed Named reconfiguration unexpectedly succeeded\n' >&2
  exit 1
fi
cmp "$ROLLBACK_ROOT/agentdock.expected" "$ROLLBACK_ROOT/config/agentdock.env"
cmp "$ROLLBACK_ROOT/cloudflared.expected" "$ROLLBACK_ROOT/config/cloudflared.env"
grep -Fq '已恢复原公网配置' "$ROLLBACK_ROOT/stderr.log"
grep -Fq 'restart agentdock' "$ROLLBACK_ROOT/systemctl.log"
grep -Fq 'enable --now agentdock-cloudflared' "$ROLLBACK_ROOT/systemctl.log"

NONE_SYSTEMD="$UPDATE_ROOT/systemd-none"
mkdir -p "$NONE_SYSTEMD/agentdock-cloudflared.service.d"
printf 'stale\n' >"$NONE_SYSTEMD/agentdock-cloudflared.service.d/retry-limit.conf"
printf 'custom\n' >"$NONE_SYSTEMD/agentdock-cloudflared.service.d/custom.conf"
printf 'AGENTDOCK_TUNNEL_MODE=quick\nTUNNEL_TOKEN=\n' >"$UPDATE_ROOT/config/cloudflared.env"
printf '2\n1\n' | PATH="$FAKE_BIN:$PATH" \
  TEST_UNAME=Linux \
  TEST_ENV_OUTPUT="$UPDATE_ROOT/none.env" \
  TEST_WRITE_ENV=true \
  AGENTDOCK_NO_SUDO=true \
  AGENTDOCK_SERVICE_MANAGER=systemd \
  AGENTDOCK_ENV_FILE="$UPDATE_ROOT/config/agentdock.env" \
  AGENTDOCK_SYSTEMD_DIR="$NONE_SYSTEMD" \
  AGENTDOCK_INSTALLER_BASE_URL="file://$RELEASE_DIR" \
  sh "$ENTRY"
assert_line 'tunnel_mode=none' "$UPDATE_ROOT/none.env"
assert_line 'existing_server_url=' "$UPDATE_ROOT/none.env"
[ ! -e "$NONE_SYSTEMD/agentdock-cloudflared.service.d/retry-limit.conf" ]
[ -f "$NONE_SYSTEMD/agentdock-cloudflared.service.d/custom.conf" ]

REPEAT_QUICK_SYSTEMD="$UPDATE_ROOT/systemd-quick-again"
mkdir -p "$REPEAT_QUICK_SYSTEMD"
printf '2\n2\n' | PATH="$FAKE_BIN:$PATH" \
  TEST_UNAME=Linux \
  TEST_ENV_OUTPUT="$UPDATE_ROOT/quick-again.env" \
  TEST_WRITE_ENV=true \
  AGENTDOCK_NO_SUDO=true \
  AGENTDOCK_SERVICE_MANAGER=systemd \
  AGENTDOCK_ENV_FILE="$UPDATE_ROOT/config/agentdock.env" \
  AGENTDOCK_SYSTEMD_DIR="$REPEAT_QUICK_SYSTEMD" \
  AGENTDOCK_INSTALLER_BASE_URL="file://$RELEASE_DIR" \
  sh "$ENTRY"
assert_line 'tunnel_mode=quick' "$UPDATE_ROOT/quick-again.env"
[ -f "$REPEAT_QUICK_SYSTEMD/agentdock-cloudflared.service.d/retry-limit.conf" ]

# 简洁、--advanced 和 OpenRC 路径都必须识别 429/1015 并停服。
RATE_ROOT="$TMP_ROOT/rate-limit"
mkdir -p "$RATE_ROOT/systemd"
if printf '2\n' | PATH="$FAKE_BIN:$PATH" \
  TEST_UNAME=Linux \
  TEST_FAIL_RATE_LIMIT=true \
  TEST_SYSTEMCTL_LOG="$RATE_ROOT/systemctl.log" \
  AGENTDOCK_NO_SUDO=true \
  AGENTDOCK_SERVICE_MANAGER=systemd \
  AGENTDOCK_ENV_FILE="$RATE_ROOT/config/agentdock.env" \
  AGENTDOCK_SYSTEMD_DIR="$RATE_ROOT/systemd" \
  AGENTDOCK_INSTALLER_BASE_URL="file://$RELEASE_DIR" \
  sh "$ENTRY" >"$RATE_ROOT/stdout.log" 2>"$RATE_ROOT/stderr.log"; then
  printf 'rate-limited installer unexpectedly succeeded\n' >&2
  exit 1
fi
grep -Fq '429/1015' "$RATE_ROOT/stderr.log"
grep -Fq 'disable --now agentdock-cloudflared' "$RATE_ROOT/systemctl.log"

ADVANCED_RATE_ROOT="$TMP_ROOT/advanced-rate-limit"
mkdir -p "$ADVANCED_RATE_ROOT/systemd"
if PATH="$FAKE_BIN:$PATH" \
  TEST_UNAME=Linux \
  TEST_FAIL_RATE_LIMIT=true \
  TEST_SYSTEMCTL_LOG="$ADVANCED_RATE_ROOT/systemctl.log" \
  AGENTDOCK_NO_SUDO=true \
  AGENTDOCK_SERVICE_MANAGER=systemd \
  AGENTDOCK_TUNNEL_MODE=quick \
  AGENTDOCK_ENV_FILE="$ADVANCED_RATE_ROOT/config/agentdock.env" \
  AGENTDOCK_SYSTEMD_DIR="$ADVANCED_RATE_ROOT/systemd" \
  AGENTDOCK_INSTALLER_BASE_URL="file://$RELEASE_DIR" \
  sh "$ENTRY" --advanced >"$ADVANCED_RATE_ROOT/stdout.log" 2>"$ADVANCED_RATE_ROOT/stderr.log"; then
  printf 'advanced rate-limited installer unexpectedly succeeded\n' >&2
  exit 1
fi
grep -Fq '429/1015' "$ADVANCED_RATE_ROOT/stderr.log"
grep -Fq 'disable --now agentdock-cloudflared' "$ADVANCED_RATE_ROOT/systemctl.log"

OPENRC_RATE_ROOT="$TMP_ROOT/openrc-rate-limit"
mkdir -p "$OPENRC_RATE_ROOT/openrc" "$OPENRC_RATE_ROOT/systemd"
if printf '2\n' | PATH="$FAKE_BIN:$PATH" \
  TEST_UNAME=Linux \
  TEST_FAIL_RATE_LIMIT=true \
  TEST_OPENRC_LOG="$OPENRC_RATE_ROOT/openrc.log" \
  AGENTDOCK_NO_SUDO=true \
  AGENTDOCK_SERVICE_MANAGER=openrc \
  AGENTDOCK_ENV_FILE="$OPENRC_RATE_ROOT/config/agentdock.env" \
  AGENTDOCK_OPENRC_DIR="$OPENRC_RATE_ROOT/openrc" \
  AGENTDOCK_SYSTEMD_DIR="$OPENRC_RATE_ROOT/systemd" \
  AGENTDOCK_INSTALLER_BASE_URL="file://$RELEASE_DIR" \
  sh "$ENTRY" >"$OPENRC_RATE_ROOT/stdout.log" 2>"$OPENRC_RATE_ROOT/stderr.log"; then
  printf 'OpenRC rate-limited installer unexpectedly succeeded\n' >&2
  exit 1
fi
grep -Fq 'rc-service agentdock-cloudflared stop' "$OPENRC_RATE_ROOT/openrc.log"
grep -Fq 'rc-update del agentdock-cloudflared default' "$OPENRC_RATE_ROOT/openrc.log"
[ ! -e "$OPENRC_RATE_ROOT/systemd/agentdock-cloudflared.service.d/retry-limit.conf" ]

# 菜单卸载默认只移除服务并保留配置、程序和数据。
MENU_UNINSTALL_ROOT="$TMP_ROOT/menu-uninstall"
mkdir -p "$MENU_UNINSTALL_ROOT/config" "$MENU_UNINSTALL_ROOT/source" "$MENU_UNINSTALL_ROOT/data" \
  "$MENU_UNINSTALL_ROOT/systemd" "$MENU_UNINSTALL_ROOT/openrc"
printf 'AGENTDOCK_HOST=127.0.0.1\n' >"$MENU_UNINSTALL_ROOT/config/agentdock.env"
printf 'unit\n' >"$MENU_UNINSTALL_ROOT/systemd/agentdock.service"
printf '3\n1\n' | PATH="$FAKE_BIN:$PATH" \
  TEST_UNAME=Linux \
  AGENTDOCK_NO_SUDO=true \
  AGENTDOCK_ENV_FILE="$MENU_UNINSTALL_ROOT/config/agentdock.env" \
  AGENTDOCK_SOURCE_DIR="$MENU_UNINSTALL_ROOT/source" \
  AGENTDOCK_DATA_DIR="$MENU_UNINSTALL_ROOT/data" \
  AGENTDOCK_SYSTEMD_DIR="$MENU_UNINSTALL_ROOT/systemd" \
  AGENTDOCK_OPENRC_DIR="$MENU_UNINSTALL_ROOT/openrc" \
  AGENTDOCK_INSTALLER_BASE_URL="file://$RELEASE_DIR" \
  sh "$ENTRY"
[ -f "$MENU_UNINSTALL_ROOT/config/agentdock.env" ]
[ -d "$MENU_UNINSTALL_ROOT/source" ]
[ -d "$MENU_UNINSTALL_ROOT/data" ]
[ ! -e "$MENU_UNINSTALL_ROOT/systemd/agentdock.service" ]

# 清配置只删 AgentDock 管理文件，不误删自定义目录中的其他内容。
UNINSTALL_ROOT="$TMP_ROOT/uninstall"
mkdir -p "$UNINSTALL_ROOT/config" "$UNINSTALL_ROOT/source" "$UNINSTALL_ROOT/data" \
  "$UNINSTALL_ROOT/systemd/agentdock-cloudflared.service.d" "$UNINSTALL_ROOT/openrc"
printf 'AGENTDOCK_HOST=127.0.0.1\n' >"$UNINSTALL_ROOT/config/agentdock.env"
printf 'AGENTDOCK_TUNNEL_MODE=named\nTUNNEL_TOKEN=token\n' >"$UNINSTALL_ROOT/config/cloudflared.env"
printf '{}\n' >"$UNINSTALL_ROOT/config/desktop-runtime.json"
printf 'keep\n' >"$UNINSTALL_ROOT/config/custom.keep"
printf 'unit\n' >"$UNINSTALL_ROOT/systemd/agentdock.service"
printf 'unit\n' >"$UNINSTALL_ROOT/systemd/agentdock-cloudflared.service"
printf 'dropin\n' >"$UNINSTALL_ROOT/systemd/agentdock-cloudflared.service.d/retry-limit.conf"
printf 'service\n' >"$UNINSTALL_ROOT/openrc/agentdock"
printf 'service\n' >"$UNINSTALL_ROOT/openrc/agentdock-cloudflared"
PATH="$FAKE_BIN:$PATH" \
  TEST_UNAME=Linux \
  AGENTDOCK_NO_SUDO=true \
  AGENTDOCK_ENV_FILE="$UNINSTALL_ROOT/config/agentdock.env" \
  AGENTDOCK_SOURCE_DIR="$UNINSTALL_ROOT/source" \
  AGENTDOCK_DATA_DIR="$UNINSTALL_ROOT/data" \
  AGENTDOCK_SYSTEMD_DIR="$UNINSTALL_ROOT/systemd" \
  AGENTDOCK_OPENRC_DIR="$UNINSTALL_ROOT/openrc" \
  AGENTDOCK_INSTALLER_BASE_URL="file://$RELEASE_DIR" \
  sh "$ENTRY" --uninstall --purge-config
[ -d "$UNINSTALL_ROOT/config" ]
[ -f "$UNINSTALL_ROOT/config/custom.keep" ]
[ ! -e "$UNINSTALL_ROOT/config/agentdock.env" ]
[ ! -e "$UNINSTALL_ROOT/config/cloudflared.env" ]
[ ! -e "$UNINSTALL_ROOT/config/desktop-runtime.json" ]
[ -d "$UNINSTALL_ROOT/source" ]
[ -d "$UNINSTALL_ROOT/data" ]
[ ! -e "$UNINSTALL_ROOT/systemd/agentdock.service" ]
[ ! -e "$UNINSTALL_ROOT/systemd/agentdock-cloudflared.service" ]
[ ! -d "$UNINSTALL_ROOT/systemd/agentdock-cloudflared.service.d" ]

# 完全卸载支持路径中的空格，只删除数据子目录并保留空数据父目录。
PURGE_ROOT="$TMP_ROOT/purge with spaces"
mkdir -p "$PURGE_ROOT/config" "$PURGE_ROOT/source/bin" "$PURGE_ROOT/data/.agentdock" "$PURGE_ROOT/systemd" "$PURGE_ROOT/openrc"
printf 'AGENTDOCK_HOST=127.0.0.1\n' >"$PURGE_ROOT/config/agentdock.env"
printf 'binary\n' >"$PURGE_ROOT/source/bin/agentdock"
PATH="$FAKE_BIN:$PATH" \
  TEST_UNAME=Linux \
  AGENTDOCK_NO_SUDO=true \
  AGENTDOCK_ENV_FILE="$PURGE_ROOT/config/agentdock.env" \
  AGENTDOCK_SOURCE_DIR="$PURGE_ROOT/source" \
  AGENTDOCK_DATA_DIR="$PURGE_ROOT/data" \
  AGENTDOCK_SYSTEMD_DIR="$PURGE_ROOT/systemd" \
  AGENTDOCK_OPENRC_DIR="$PURGE_ROOT/openrc" \
  AGENTDOCK_INSTALLER_BASE_URL="file://$RELEASE_DIR" \
  sh "$ENTRY" --uninstall --purge-data
[ ! -d "$PURGE_ROOT/config" ]
[ ! -d "$PURGE_ROOT/source" ]
[ -d "$PURGE_ROOT/data" ]
[ ! -e "$PURGE_ROOT/data/.agentdock" ]
[ ! -e "$PURGE_ROOT/data/AgentDock" ]

# 共享数据父目录中的非 AgentDock 文件必须保留。
SHARED_ROOT="$TMP_ROOT/shared-data"
mkdir -p "$SHARED_ROOT/config" "$SHARED_ROOT/source/bin" "$SHARED_ROOT/data/.agentdock" \
  "$SHARED_ROOT/data/AgentDock" "$SHARED_ROOT/systemd" "$SHARED_ROOT/openrc"
printf 'AGENTDOCK_HOST=127.0.0.1\n' >"$SHARED_ROOT/config/agentdock.env"
printf 'binary\n' >"$SHARED_ROOT/source/bin/agentdock"
printf 'keep\n' >"$SHARED_ROOT/data/custom.keep"
PATH="$FAKE_BIN:$PATH" \
  TEST_UNAME=Linux \
  AGENTDOCK_NO_SUDO=true \
  AGENTDOCK_ENV_FILE="$SHARED_ROOT/config/agentdock.env" \
  AGENTDOCK_SOURCE_DIR="$SHARED_ROOT/source" \
  AGENTDOCK_DATA_DIR="$SHARED_ROOT/data" \
  AGENTDOCK_SYSTEMD_DIR="$SHARED_ROOT/systemd" \
  AGENTDOCK_OPENRC_DIR="$SHARED_ROOT/openrc" \
  AGENTDOCK_INSTALLER_BASE_URL="file://$RELEASE_DIR" \
  sh "$ENTRY" --uninstall --purge-data
[ ! -d "$SHARED_ROOT/source" ]
[ -f "$SHARED_ROOT/data/custom.keep" ]
[ ! -e "$SHARED_ROOT/data/.agentdock" ]
[ ! -e "$SHARED_ROOT/data/AgentDock" ]

# 默认使用 passwd 中的安装用户 HOME；sudo 和显式运行用户按优先级选择。
ACCOUNT_BIN="$TMP_ROOT/account-bin"
mkdir -p "$ACCOUNT_BIN"
REAL_ID="$(command -v id)"
REAL_RMDIR="$(command -v rmdir)"
export REAL_ID REAL_RMDIR
cat >"$ACCOUNT_BIN/id" <<'SH'
#!/bin/sh
if [ "$*" = '-un' ]; then
  printf 'installer\n'
else
  exec "$REAL_ID" "$@"
fi
SH
cat >"$ACCOUNT_BIN/getent" <<'SH'
#!/bin/sh
[ "$1" = passwd ] || exit 1
case "$2" in
  installer|sudoer|chosen) printf '%s:x:1000:1000::%s/%s:/bin/sh\n' "$2" "$TEST_ACCOUNT_ROOT" "$2" ;;
  *) exit 2 ;;
esac
SH
cat >"$ACCOUNT_BIN/rmdir" <<'SH'
#!/bin/sh
printf '%s\n' "$*" >>"$TEST_RMDIR_LOG"
exec "$REAL_RMDIR" "$@"
SH
for command_name in chown userdel deluser; do
  cat >"$ACCOUNT_BIN/$command_name" <<'SH'
#!/bin/sh
printf '%s %s\n' "$(basename "$0")" "$*" >>"$TEST_FORBIDDEN_LOG"
exit 1
SH
done
chmod +x "$ACCOUNT_BIN"/*

for account_case in default sudo explicit override empty; do
  ACCOUNT_ROOT="$TMP_ROOT/account-$account_case"
  case "$account_case" in
    sudo) selected_account=sudoer ;;
    explicit|override) selected_account=chosen ;;
    *) selected_account=installer ;;
  esac
  account_home="$ACCOUNT_ROOT/$selected_account"
  selected_data="$account_home"
  [ "$account_case" != override ] || selected_data="$ACCOUNT_ROOT/custom-data"
  for account in installer sudoer chosen; do
    mkdir -p "$ACCOUNT_ROOT/$account/.agentdock" "$ACCOUNT_ROOT/$account/AgentDock"
    if [ "$account_case" != empty ]; then
      printf 'keep\n' >"$ACCOUNT_ROOT/$account/personal.keep"
    fi
  done
  mkdir -p "$ACCOUNT_ROOT/config" "$ACCOUNT_ROOT/source/bin" "$ACCOUNT_ROOT/systemd" \
    "$ACCOUNT_ROOT/openrc" "$selected_data/.agentdock" "$selected_data/AgentDock"
  printf 'binary\n' >"$ACCOUNT_ROOT/source/bin/agentdock"
  printf 'config\n' >"$ACCOUNT_ROOT/config/agentdock.env"
  (
    unset SUDO_USER AGENTDOCK_SERVICE_USER AGENTDOCK_DATA_DIR
    case "$account_case" in
      sudo) export SUDO_USER=sudoer ;;
      explicit|override) export SUDO_USER=sudoer AGENTDOCK_SERVICE_USER=chosen ;;
    esac
    if [ "$account_case" = override ]; then
      export AGENTDOCK_DATA_DIR="$selected_data"
    fi
    PATH="$ACCOUNT_BIN:$FAKE_BIN:$PATH" \
      HOME="$ACCOUNT_ROOT/inherited-wrong-home" \
      TEST_ACCOUNT_ROOT="$ACCOUNT_ROOT" \
      TEST_RMDIR_LOG="$ACCOUNT_ROOT/rmdir.log" \
      TEST_FORBIDDEN_LOG="$ACCOUNT_ROOT/forbidden.log" \
      AGENTDOCK_NO_SUDO=true \
      AGENTDOCK_ENV_FILE="$ACCOUNT_ROOT/config/agentdock.env" \
      AGENTDOCK_SOURCE_DIR="$ACCOUNT_ROOT/source" \
      AGENTDOCK_SYSTEMD_DIR="$ACCOUNT_ROOT/systemd" \
      AGENTDOCK_OPENRC_DIR="$ACCOUNT_ROOT/openrc" \
      sh "$RELEASE_DIR/uninstall-linux.sh" --purge-data
  )
  [ -d "$account_home" ]
  [ -d "$selected_data" ]
  [ ! -e "$selected_data/.agentdock" ]
  [ ! -e "$selected_data/AgentDock" ]
  [ ! -e "$ACCOUNT_ROOT/forbidden.log" ]
  ! grep -Fqx "$account_home" "$ACCOUNT_ROOT/rmdir.log"
  ! grep -Fqx "$selected_data" "$ACCOUNT_ROOT/rmdir.log"
  for account in installer sudoer chosen; do
    if [ "$account_case" != empty ]; then
      [ -f "$ACCOUNT_ROOT/$account/personal.keep" ]
    fi
    if [ "$ACCOUNT_ROOT/$account" != "$selected_data" ]; then
      [ -d "$ACCOUNT_ROOT/$account/.agentdock" ]
      [ -d "$ACCOUNT_ROOT/$account/AgentDock" ]
    fi
  done
done

# HOME 可包含独立的程序和配置目录，但待删除子目录不能与它们重叠。
# 任何路径拒绝都必须发生在停服和删除配置之前。
for protection_case in siblings source-home config-home source-overlap config-overlap data-link managed-link data-traversal; do
  PROTECTION_ROOT="$TMP_ROOT/protection-$protection_case"
  account_home="$PROTECTION_ROOT/chosen"
  selected_source="$account_home/.local/agentdock"
  selected_config="$account_home/.config/agentdock"
  selected_data="$account_home"
  mkdir -p "$selected_source/bin" "$selected_config" "$account_home/.agentdock" \
    "$account_home/AgentDock" "$PROTECTION_ROOT/outside" "$PROTECTION_ROOT/systemd" "$PROTECTION_ROOT/openrc"
  printf 'keep\n' >"$PROTECTION_ROOT/outside/personal.keep"
  printf 'unit\n' >"$PROTECTION_ROOT/systemd/agentdock.service"
  case "$protection_case" in
    source-home) selected_source="$account_home" ;;
    config-home) selected_config="$account_home" ;;
    source-overlap) selected_source="$account_home/AgentDock/source" ;;
    config-overlap) selected_config="$account_home/.agentdock/config" ;;
    data-link)
      ln -s "$account_home" "$PROTECTION_ROOT/home-link"
      selected_data="$PROTECTION_ROOT/home-link"
      ;;
    managed-link)
      rmdir "$account_home/.agentdock"
      ln -s "$PROTECTION_ROOT/outside" "$account_home/.agentdock"
      ;;
    data-traversal) selected_data="$account_home/child/.." ;;
  esac
  mkdir -p "$selected_source/bin" "$selected_config"
  printf 'binary\n' >"$selected_source/bin/agentdock"
  printf 'config\n' >"$selected_config/agentdock.env"
  if PATH="$ACCOUNT_BIN:$FAKE_BIN:$PATH" \
    TEST_ACCOUNT_ROOT="$PROTECTION_ROOT" \
    TEST_RMDIR_LOG="$PROTECTION_ROOT/rmdir.log" \
    TEST_FORBIDDEN_LOG="$PROTECTION_ROOT/forbidden.log" \
    AGENTDOCK_NO_SUDO=true \
    AGENTDOCK_SERVICE_USER=chosen \
    AGENTDOCK_ENV_FILE="$selected_config/agentdock.env" \
    AGENTDOCK_SOURCE_DIR="$selected_source" \
    AGENTDOCK_DATA_DIR="$selected_data" \
    AGENTDOCK_SYSTEMD_DIR="$PROTECTION_ROOT/systemd" \
    AGENTDOCK_OPENRC_DIR="$PROTECTION_ROOT/openrc" \
    sh "$RELEASE_DIR/uninstall-linux.sh" --purge-data >"$PROTECTION_ROOT/stdout.log" 2>"$PROTECTION_ROOT/stderr.log"; then
    [ "$protection_case" = siblings ] || {
      printf 'uninstaller accepted unsafe paths: %s\n' "$protection_case" >&2
      exit 1
    }
    [ -d "$account_home" ]
    [ ! -e "$account_home/.agentdock" ]
    [ ! -e "$account_home/AgentDock" ]
    [ ! -e "$selected_source" ]
    [ ! -e "$selected_config" ]
  else
    [ "$protection_case" != siblings ]
    grep -Eq '拒绝删除用户主目录|拒绝清除.*重叠|软链接或非规范路径|路径跳转' "$PROTECTION_ROOT/stderr.log"
    [ -f "$PROTECTION_ROOT/systemd/agentdock.service" ]
    [ -f "$selected_source/bin/agentdock" ]
    [ -f "$selected_config/agentdock.env" ]
  fi
  [ -f "$PROTECTION_ROOT/outside/personal.keep" ]
done

# 危险、相对、跳转和软链接路径必须在停服前拒绝。
SAFE_SERVICE_ROOT="$TMP_ROOT/safe-services"
mkdir -p "$SAFE_SERVICE_ROOT/systemd" "$SAFE_SERVICE_ROOT/openrc"
if PATH="$FAKE_BIN:$PATH" \
  TEST_UNAME=Linux \
  AGENTDOCK_NO_SUDO=true \
  AGENTDOCK_ENV_FILE=/agentdock.env \
  AGENTDOCK_SYSTEMD_DIR="$SAFE_SERVICE_ROOT/systemd" \
  AGENTDOCK_OPENRC_DIR="$SAFE_SERVICE_ROOT/openrc" \
  AGENTDOCK_INSTALLER_BASE_URL="file://$RELEASE_DIR" \
  sh "$ENTRY" --uninstall --purge-config >"$TMP_ROOT/dangerous.out" 2>"$TMP_ROOT/dangerous.err"; then
  printf 'uninstaller accepted a dangerous config path\n' >&2
  exit 1
fi
grep -Fq '拒绝删除危险路径' "$TMP_ROOT/dangerous.err"

if PATH="$FAKE_BIN:$PATH" \
  TEST_UNAME=Linux \
  AGENTDOCK_NO_SUDO=true \
  AGENTDOCK_ENV_FILE="$TMP_ROOT/safe-config/agentdock.env" \
  AGENTDOCK_SOURCE_DIR=/opt/agentdock/../.. \
  AGENTDOCK_DATA_DIR="$TMP_ROOT/safe-data" \
  AGENTDOCK_SYSTEMD_DIR="$SAFE_SERVICE_ROOT/systemd" \
  AGENTDOCK_OPENRC_DIR="$SAFE_SERVICE_ROOT/openrc" \
  AGENTDOCK_INSTALLER_BASE_URL="file://$RELEASE_DIR" \
  sh "$ENTRY" --uninstall --purge-data >"$TMP_ROOT/traversal.out" 2>"$TMP_ROOT/traversal.err"; then
  printf 'uninstaller accepted a traversal path\n' >&2
  exit 1
fi
grep -Fq '路径跳转' "$TMP_ROOT/traversal.err"

if PATH="$FAKE_BIN:$PATH" \
  TEST_UNAME=Linux \
  AGENTDOCK_NO_SUDO=true \
  AGENTDOCK_ENV_FILE="$TMP_ROOT/safe-config/agentdock.env" \
  AGENTDOCK_SOURCE_DIR=relative/source \
  AGENTDOCK_DATA_DIR="$TMP_ROOT/safe-data" \
  AGENTDOCK_SYSTEMD_DIR="$SAFE_SERVICE_ROOT/systemd" \
  AGENTDOCK_OPENRC_DIR="$SAFE_SERVICE_ROOT/openrc" \
  AGENTDOCK_INSTALLER_BASE_URL="file://$RELEASE_DIR" \
  sh "$ENTRY" --uninstall --purge-data >"$TMP_ROOT/relative.out" 2>"$TMP_ROOT/relative.err"; then
  printf 'uninstaller accepted a relative path\n' >&2
  exit 1
fi
grep -Fq '必须是绝对路径' "$TMP_ROOT/relative.err"

SYMLINK_ROOT="$TMP_ROOT/symlink"
mkdir -p "$SYMLINK_ROOT/target" "$SYMLINK_ROOT/data" "$SYMLINK_ROOT/config"
ln -s "$SYMLINK_ROOT/target" "$SYMLINK_ROOT/source-link"
if PATH="$FAKE_BIN:$PATH" \
  TEST_UNAME=Linux \
  AGENTDOCK_NO_SUDO=true \
  AGENTDOCK_ENV_FILE="$SYMLINK_ROOT/config/agentdock.env" \
  AGENTDOCK_SOURCE_DIR="$SYMLINK_ROOT/source-link" \
  AGENTDOCK_DATA_DIR="$SYMLINK_ROOT/data" \
  AGENTDOCK_SYSTEMD_DIR="$SAFE_SERVICE_ROOT/systemd" \
  AGENTDOCK_OPENRC_DIR="$SAFE_SERVICE_ROOT/openrc" \
  AGENTDOCK_INSTALLER_BASE_URL="file://$RELEASE_DIR" \
  sh "$ENTRY" --uninstall --purge-data >"$TMP_ROOT/symlink.out" 2>"$TMP_ROOT/symlink.err"; then
  printf 'uninstaller accepted a symlink removal path\n' >&2
  exit 1
fi
grep -Fq '软链接或非规范路径' "$TMP_ROOT/symlink.err"
[ -d "$SYMLINK_ROOT/target" ]

UNRECOGNIZED_ROOT="$TMP_ROOT/unrecognized-source"
mkdir -p "$UNRECOGNIZED_ROOT/config" "$UNRECOGNIZED_ROOT/source" "$UNRECOGNIZED_ROOT/data/.agentdock" \
  "$UNRECOGNIZED_ROOT/systemd" "$UNRECOGNIZED_ROOT/openrc"
printf 'do-not-delete\n' >"$UNRECOGNIZED_ROOT/source/custom.keep"
printf 'unit\n' >"$UNRECOGNIZED_ROOT/systemd/agentdock.service"
if PATH="$FAKE_BIN:$PATH" \
  TEST_UNAME=Linux \
  AGENTDOCK_NO_SUDO=true \
  AGENTDOCK_ENV_FILE="$UNRECOGNIZED_ROOT/config/agentdock.env" \
  AGENTDOCK_SOURCE_DIR="$UNRECOGNIZED_ROOT/source" \
  AGENTDOCK_DATA_DIR="$UNRECOGNIZED_ROOT/data" \
  AGENTDOCK_SYSTEMD_DIR="$UNRECOGNIZED_ROOT/systemd" \
  AGENTDOCK_OPENRC_DIR="$UNRECOGNIZED_ROOT/openrc" \
  AGENTDOCK_INSTALLER_BASE_URL="file://$RELEASE_DIR" \
  sh "$ENTRY" --uninstall --purge-data >"$TMP_ROOT/unrecognized.out" 2>"$TMP_ROOT/unrecognized.err"; then
  printf 'uninstaller accepted an unrecognized source directory\n' >&2
  exit 1
fi
grep -Fq '无法识别为 AgentDock' "$TMP_ROOT/unrecognized.err"
[ -f "$UNRECOGNIZED_ROOT/source/custom.keep" ]
[ -f "$UNRECOGNIZED_ROOT/systemd/agentdock.service" ]

if PATH="$FAKE_BIN:$PATH" \
  TEST_UNAME=Linux \
  AGENTDOCK_NO_SUDO=true \
  AGENTDOCK_SERVICE_NAME='../unsafe' \
  AGENTDOCK_ENV_FILE="$TMP_ROOT/safe-config/agentdock.env" \
  AGENTDOCK_SYSTEMD_DIR="$SAFE_SERVICE_ROOT/systemd" \
  AGENTDOCK_OPENRC_DIR="$SAFE_SERVICE_ROOT/openrc" \
  AGENTDOCK_INSTALLER_BASE_URL="file://$RELEASE_DIR" \
  sh "$ENTRY" --uninstall >"$TMP_ROOT/service-name.out" 2>"$TMP_ROOT/service-name.err"; then
  printf 'uninstaller accepted an unsafe service name\n' >&2
  exit 1
fi
grep -Fq '服务名包含不安全字符' "$TMP_ROOT/service-name.err"

write_fake_platform install-macos-platform.sh
MACOS_OUTPUT="$TMP_ROOT/macos.args"
PATH="$FAKE_BIN:$PATH" \
  TEST_UNAME=Darwin \
  TEST_OUTPUT="$MACOS_OUTPUT" \
  AGENTDOCK_INSTALLER_BASE_URL="file://$RELEASE_DIR" \
  sh "$ENTRY" --mode "value with spaces"
assert_args "$MACOS_OUTPUT"

printf '%s\n' '0000000000000000000000000000000000000000000000000000000000000000  install-linux-platform.sh' \
  >"$RELEASE_DIR/install-linux-platform.sh.sha256"
if PATH="$FAKE_BIN:$PATH" \
  TEST_UNAME=Linux \
  AGENTDOCK_NO_SUDO=true \
  AGENTDOCK_ENV_FILE="$TMP_ROOT/invalid/config/agentdock.env" \
  AGENTDOCK_INSTALLER_BASE_URL="file://$RELEASE_DIR" \
  sh "$ENTRY" >/dev/null 2>&1; then
  printf 'installer accepted an invalid checksum\n' >&2
  exit 1
fi

if PATH="$FAKE_BIN:$PATH" TEST_UNAME=FreeBSD sh "$ENTRY" >/dev/null 2>&1; then
  printf 'installer accepted an unsupported operating system\n' >&2
  exit 1
fi

printf 'unified installer entry tests passed\n'
