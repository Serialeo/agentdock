#!/bin/zsh
set -euo pipefail

LABEL="com.uvwt.agentdock"
TUNNEL_LABEL="com.uvwt.agentdock.cloudflared"
APP_SUPPORT_DIR="$HOME/Library/Application Support/AgentDock"
PLIST_PATH="$HOME/Library/LaunchAgents/$LABEL.plist"
TUNNEL_PLIST_PATH="$HOME/Library/LaunchAgents/$TUNNEL_LABEL.plist"
LOG_DIR="$HOME/Library/Logs/AgentDock"
BINARY_PATH="$HOME/.local/bin/agentdock"
CLOUDFLARED_BINARY_PATH="$HOME/.local/bin/cloudflared"
STATE_DIR="$HOME/.agentdock"
WORK_DIR="$HOME/AgentDock"
REMOVE_BINARY=false
PURGE_DATA=false

die() {
  print -u2 -- "ERROR: $*"
  exit 1
}

usage() {
  cat <<'USAGE'
AgentDock macOS 卸载脚本。

用法：
  zsh uninstall-macos.sh [--remove-binary] [--purge-data]

默认行为：
  停止并删除 LaunchAgent、服务支持文件和日志；保留二进制、~/.agentdock 与 ~/AgentDock。

选项：
  --remove-binary  同时删除 ~/.local/bin/agentdock 和安装器管理的 cloudflared
  --purge-data     彻底卸载，同时删除二进制、~/.agentdock 与 ~/AgentDock
  -h, --help       显示帮助
USAGE
}

while (( $# > 0 )); do
  case "$1" in
    --remove-binary)
      REMOVE_BINARY=true
      shift
      ;;
    --purge-data)
      PURGE_DATA=true
      REMOVE_BINARY=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      print -u2 -- "ERROR: 未知参数：$1"
      exit 2
      ;;
  esac
done

[[ "$(uname -s)" == "Darwin" ]] || { print -u2 -- "ERROR: 此脚本只支持 macOS"; exit 1; }

stop_launch_agent() {
  local domain="$1"
  local label="$2"
  local bootout_error
  if launchctl print "$domain/$label" >/dev/null 2>&1; then
    bootout_error="$(launchctl bootout "$domain/$label" 2>&1)" || die "停止 LaunchAgent 失败：$label ${bootout_error:-unknown error}"
    if launchctl print "$domain/$label" >/dev/null 2>&1; then
      die "LaunchAgent 仍在运行，未删除任何服务文件：$label"
    fi
  fi
}

domain="gui/$(id -u)"
stop_launch_agent "$domain" "$TUNNEL_LABEL"
stop_launch_agent "$domain" "$LABEL"
rm -f "$PLIST_PATH" "$TUNNEL_PLIST_PATH"
rm -rf "$APP_SUPPORT_DIR" "$LOG_DIR"
print -- "removed service: $LABEL"

if [[ "$REMOVE_BINARY" == true ]]; then
  rm -f "$BINARY_PATH" "$CLOUDFLARED_BINARY_PATH"
  print -- "removed binary: $BINARY_PATH"
  print -- "removed binary: $CLOUDFLARED_BINARY_PATH"
else
  print -- "preserved binary: $BINARY_PATH"
  print -- "preserved binary: $CLOUDFLARED_BINARY_PATH"
fi

if [[ "$PURGE_DATA" == true ]]; then
  rm -rf "$STATE_DIR" "$WORK_DIR"
  print -- "removed data: $STATE_DIR"
  print -- "removed work directory: $WORK_DIR"
else
  print -- "preserved data: $STATE_DIR"
  print -- "preserved work directory: $WORK_DIR"
fi
