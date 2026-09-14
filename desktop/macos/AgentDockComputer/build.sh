#!/bin/bash
set -euo pipefail
if [[ "$(uname -s)" != Darwin ]]; then
  echo '请在 macOS 13 或更新版本上运行此脚本。' >&2
  exit 1
fi
source_dir="$(cd "$(dirname "$0")" && pwd)"
repo_root="$(cd "$source_dir/../../.." && pwd)"
output_dir="$repo_root/dist/computer-spike-macos"
app="$output_dir/AgentDockComputer.app"
sdk="$(xcrun --sdk macosx --show-sdk-path)"
arch="$(uname -m)"
case "$arch" in arm64|x86_64) ;; *) echo "Unsupported architecture: $arch" >&2; exit 1;; esac
mkdir -p "$output_dir"
build_dir="$(mktemp -d "$output_dir/.build.XXXXXX")"
trap 'rm -rf "$build_dir"' EXIT
xcrun swiftc -swift-version 5 -sdk "$sdk" -target "$arch-apple-macosx13.0" \
  "$source_dir/Sources/Geometry.swift" "$source_dir/Tests/GeometryTests.swift" \
  -o "$build_dir/geometry-tests"
"$build_dir/geometry-tests"
# main.swift uses an explicit @main entry point instead of top-level statements.
xcrun swiftc -parse-as-library -swift-version 5 -sdk "$sdk" -target "$arch-apple-macosx13.0" \
  "$source_dir/Sources/Geometry.swift" "$source_dir/Sources/Capture.swift" "$source_dir/Sources/main.swift" \
  -framework AppKit -framework ApplicationServices -framework ScreenCaptureKit -framework CoreImage \
  -o "$build_dir/AgentDockComputer"
mkdir -p "$app/Contents/MacOS"
mv "$build_dir/AgentDockComputer" "$app/Contents/MacOS/AgentDockComputer"
cat > "$app/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>AgentDockComputer</string>
<key>CFBundleIdentifier</key><string>com.uvwt.agentdock.computer.spike</string>
<key>CFBundleName</key><string>AgentDockComputer</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleShortVersionString</key><string>0.1.0</string>
<key>CFBundleVersion</key><string>1</string>
<key>LSMinimumSystemVersion</key><string>13.0</string>
<key>NSHighResolutionCapable</key><true/>
</dict></plist>
PLIST
codesign --force --sign "${AGENTDOCK_COMPUTER_SIGN_IDENTITY:--}" "$app"
codesign --verify --strict "$app"
printf '\n构建完成。请保持 App 路径不变，用以下命令打开：\nopen "%s"\n' "$app"
printf '默认使用 ad-hoc 本地签名；不代表已完成 Developer ID / notarization 验证。\n'
