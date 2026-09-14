#!/bin/bash
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
output="${1:-$root/dist/AgentDockComputer.app}"
mkdir -p "$output/Contents/MacOS"
cd "$root"
CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -tags agentdock_computer_native -trimpath -o "$output/Contents/MacOS/agentdock-computer" ./cmd/agentdock-computer
cat > "$output/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.uvwt.agentdock.computer</string>
<key>CFBundleName</key><string>AgentDock Computer</string>
<key>CFBundleExecutable</key><string>agentdock-computer</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleVersion</key><string>1</string>
<key>LSMinimumSystemVersion</key><string>13.0</string>
<key>LSUIElement</key><true/>
<key>NSHighResolutionCapable</key><true/>
</dict></plist>
PLIST
codesign --force --sign "${AGENTDOCK_MACOS_SIGN_IDENTITY:--}" --identifier com.uvwt.agentdock.computer "$output"
