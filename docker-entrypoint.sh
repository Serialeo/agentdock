#!/usr/bin/env sh
set -eu

umask 077
mkdir -p \
  "$HOME/.agentdock/tmp" \
  "$HOME/AgentDock"

# Only seed an unconfigured browser image. Runtime choices are owned by the core thereafter.
capability_home="${AGENTDOCK_HOME:-$HOME/.agentdock}"
if [ -f /usr/local/share/agentdock/builtin-defaults.json ] && [ ! -e "$capability_home/builtin-capabilities.json" ]; then
  mkdir -p "$capability_home"
  cp -n /usr/local/share/agentdock/builtin-defaults.json "$capability_home/builtin-capabilities.json"
fi

if [ "${1:-}" = "agentdock" ]; then
  case "${2:-}" in
    --version|update|skill) ;;
    *) agentdock skill bootstrap --bundle /usr/local/share/agentdock/core-skills ;;
  esac
fi

exec "$@"
