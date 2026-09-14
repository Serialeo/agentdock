import AppKit
import ApplicationServices
import ScreenCaptureKit
import CoreGraphics
import Darwin

let injectionTag: Int64 = 0x41444350
final class CalibrationView: NSView {
    var hits = 0
    var armed = false
    override func draw(_ dirtyRect: NSRect) {
        let color = hits == 0 ? NSColor(deviceRed: 20/255.0, green: 180/255.0, blue: 60/255.0, alpha: 1)
                              : NSColor(deviceRed: 20/255.0, green: 60/255.0, blue: 210/255.0, alpha: 1)
        color.setFill(); bounds.fill()
    }
    override func mouseUp(with event: NSEvent) {
        guard armed, event.cgEvent?.getIntegerValueField(.eventSourceUserData) == injectionTag else { return }
        hits += 1; needsDisplay = true; displayIfNeeded()
    }
}

@MainActor final class SpikeApp: NSObject, NSApplicationDelegate {
    private var window: NSWindow!
    private let target = CalibrationView(frame: NSRect(x: 80, y: 70, width: 280, height: 160))
    private let status = NSTextField(wrappingLabelWithString: "")
    private var task: Task<Void, Never>?
    private var output: URL?

    func applicationDidFinishLaunching(_ notification: Notification) {
        window = NSWindow(contentRect: NSRect(x: 100, y: 100, width: 820, height: 420),
                          styleMask: [.titled, .closable, .miniaturizable], backing: .buffered, defer: false)
        window.title = "AgentDock Computer P0 — own calibration target only"
        window.isReleasedWhenClosed = false
        let content = window.contentView!
        for (title, action, x, width) in [("检查权限", #selector(refresh), 20, 100), ("请求权限", #selector(requestPermissions), 130, 100),
                                          ("运行一次验证", #selector(run), 240, 140), ("停止", #selector(stop), 390, 90),
                                          ("打开报告目录", #selector(openReports), 490, 150)] {
            let button = NSButton(title: title, target: self, action: action)
            button.frame = NSRect(x: CGFloat(x), y: 365, width: CGFloat(width), height: 32); content.addSubview(button)
        }
        status.frame = NSRect(x: 20, y: 250, width: 780, height: 105)
        content.addSubview(status); content.addSubview(target)
        let menu = NSMenu(); let appItem = NSMenuItem(); menu.addItem(appItem)
        let appMenu = NSMenu(); appMenu.addItem(withTitle: "Quit", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        appItem.submenu = appMenu; NSApp.mainMenu = menu
        window.makeKeyAndOrderFront(nil); NSApp.activate(ignoringOtherApps: true); refresh()
    }
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { true }
    func applicationWillTerminate(_ notification: Notification) { task?.cancel(); target.armed = false }
    @objc private func refresh() {
        guard task == nil else { return }
        status.stringValue = "实际执行组件：\(Bundle.main.bundleIdentifier ?? "unbundled")\n辅助功能：\(AXIsProcessTrusted())；屏幕录制：\(CGPreflightScreenCaptureAccess())\n将整个绿色区移到待测显示器后运行；截屏可能包含该显示器上的其他内容，仅保存本机。"
    }
    @objc private func requestPermissions() {
        guard task == nil else { return }
        _ = AXIsProcessTrustedWithOptions([kAXTrustedCheckOptionPrompt.takeUnretainedValue() as String: true] as CFDictionary)
        _ = CGRequestScreenCaptureAccess()
        refresh()
    }
    @objc private func stop() { task?.cancel(); target.armed = false; status.stringValue = "已请求停止；不会重发点击。" }
    @objc private func openReports() {
        let root = reportsRoot()
        try? FileManager.default.createDirectory(at: root, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        NSWorkspace.shared.open(root)
    }
    private func reportsRoot() -> URL {
        FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("AgentDock/computer-spike", isDirectory: true)
    }
    @objc private func run() {
        guard task == nil else { return }
        task = Task { @MainActor in
            await runProbe()
            task = nil
        }
    }
    private func quartzRect() throws -> CGRect {
        guard let primary = NSScreen.screens.first else { throw ProbeFailure("no screen") }
        let rect = window.convertToScreen(target.convert(target.bounds, to: nil))
        return CGRect(x: rect.minX, y: primary.frame.maxY - rect.maxY, width: rect.width, height: rect.height)
    }
    private func requireDesktop() throws {
        guard let session = CGSessionCopyCurrentDictionary() as? [String: Any],
              (session[kCGSessionOnConsoleKey as String] as? Bool) == true,
              (session["CGSSessionScreenIsLocked"] as? Bool) != true else { throw ProbeFailure("not an unlocked console session") }
    }
    @MainActor private func runProbe() async {
        var report: [String: Any] = ["schema_version": 1, "probe": "self_target_click", "platform": "macos", "passed": false,
            "input_attempted": false, "input_status": "not_submitted", "bundle_id": Bundle.main.bundleIdentifier ?? "unbundled",
            "executable": Bundle.main.executablePath ?? "unknown", "os_version": ProcessInfo.processInfo.operatingSystemVersionString,
            "accessibility_granted": AXIsProcessTrusted(), "screen_recording_granted": CGPreflightScreenCaptureAccess()]
        var stream: SCStream?
        let directory = reportsRoot().appendingPathComponent(UUID().uuidString, isDirectory: true)
        output = directory
        do {
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
            guard AXIsProcessTrusted(), CGPreflightScreenCaptureAccess() else { throw ProbeFailure("请先给此测试 App 授予辅助功能和屏幕录制权限，必要时退出重开") }
            try requireDesktop(); try Task.checkCancellation()
            status.stringValue = "准备 3 秒后验证；现在可点击停止。之后请勿移动窗口或操作鼠标。"
            try await Task.sleep(nanoseconds: 3_000_000_000)
            guard let screen = window.screen,
                  let number = screen.deviceDescription[NSDeviceDescriptionKey("NSScreenNumber")] as? NSNumber else { throw ProbeFailure("window has no display") }
            let displayID = CGDirectDisplayID(number.uint32Value)
            guard CGDisplayRotation(displayID) == 0 else { throw ProbeFailure("P0 does not yet support rotated displays") }
            let bounds = CGDisplayBounds(displayID), expected = try quartzRect()
            guard bounds.contains(expected) else { throw ProbeFailure("place the entire calibration target on one display") }
            let content = try await SCShareableContent.excludingDesktopWindows(false, onScreenWindowsOnly: true)
            try Task.checkCancellation()
            guard let display = content.displays.first(where: { $0.displayID == displayID }) else { throw ProbeFailure("display not available to ScreenCaptureKit") }
            let width = CGDisplayPixelsWide(displayID), height = CGDisplayPixelsHigh(displayID)
            guard width > 0, height > 0, width * height <= 40_000_000 else { throw ProbeFailure("capture exceeds P0 pixel limit") }
            let configuration = SCStreamConfiguration()
            configuration.width = width; configuration.height = height
            configuration.pixelFormat = kCVPixelFormatType_32BGRA
            configuration.minimumFrameInterval = CMTime(value: 1, timescale: 10)
            configuration.queueDepth = 3; configuration.showsCursor = false; configuration.capturesAudio = false
            let collector = FrameCollector()
            let capture = SCStream(filter: SCContentFilter(display: display, excludingWindows: []), configuration: configuration, delegate: collector)
            stream = capture
            try capture.addStreamOutput(collector, type: .screen, sampleHandlerQueue: DispatchQueue(label: "computer.spike.capture"))
            target.hits = 0; target.armed = false; target.needsDisplay = true; target.displayIfNeeded()
            status.stringValue = "正在验证：截图 → 单次系统输入 → 新截图。请勿移动窗口或点击校准区。"
            let started = mach_absolute_time()
            try await capture.startCapture()
            let before = try await collector.next(after: started)
            try savePNG(before, to: directory.appendingPathComponent("before.png"))
            report["capture_content_rect"] = [before.contentRect.minX, before.contentRect.minY, before.contentRect.width, before.contentRect.height]
            report["capture_scale_factor"] = before.scaleFactor
            report["capture_content_scale"] = before.contentScale
            try validateFullDisplayContent(before.contentRect, scaleFactor: before.scaleFactor, width: before.image.width, height: before.image.height)
            let geometry = CaptureGeometry(width: before.image.width, height: before.image.height, desktop: bounds)
            let center = CGPoint(x: expected.midX, y: expected.midY)
            let imagePoint = try geometry.image(center)
            let native = try geometry.native(imagePoint)
            let samplePoint = try geometry.image(CGPoint(x: expected.minX + 30, y: expected.minY + 30))
            let oldRGB = try sampleRGB(before.image, at: samplePoint)
            guard oldRGB[1] > oldRGB[0] + 50 && oldRGB[1] > oldRGB[2] + 50 else { throw ProbeFailure("initial target is not green; check capture mapping or occlusion") }
            report["image"] = ["width": before.image.width, "height": before.image.height]
            report["desktop_bounds"] = [bounds.minX, bounds.minY, bounds.maxX, bounds.maxY]
            report["image_point"] = [imagePoint.x, imagePoint.y]; report["native_point"] = [native.x, native.y]
            report["before_display_ticks"] = String(before.displayTime)
            try Task.checkCancellation(); try requireDesktop()
            guard AXIsProcessTrusted(), CGPreflightScreenCaptureAccess(), NSApp.isActive, window.isKeyWindow,
                  try quartzRect() == expected, CGDisplayBounds(displayID) == bounds else { throw ProbeFailure("permissions, focus or display/window geometry changed") }
            let systemElement = AXUIElementCreateSystemWide()
            var hitElement: AXUIElement?
            guard AXUIElementCopyElementAtPosition(systemElement, Float(native.x), Float(native.y), &hitElement) == .success,
                  let element = hitElement else { throw ProbeFailure("cannot identify the window under the target point") }
            var hitPID: pid_t = 0
            guard AXUIElementGetPid(element, &hitPID) == .success, hitPID == getpid() else { throw ProbeFailure("target point is occluded by another application") }
            try Task.checkCancellation()
            guard !CGEventSource.buttonState(.combinedSessionState, button: .left) else { throw ProbeFailure("physical mouse button still held") }
            guard let source = CGEventSource(stateID: .hidSystemState),
                  let down = CGEvent(mouseEventSource: source, mouseType: .leftMouseDown, mouseCursorPosition: native, mouseButton: .left),
                  let up = CGEvent(mouseEventSource: source, mouseType: .leftMouseUp, mouseCursorPosition: native, mouseButton: .left) else { throw ProbeFailure("could not create native mouse events") }
            down.setIntegerValueField(.eventSourceUserData, value: injectionTag)
            up.setIntegerValueField(.eventSourceUserData, value: injectionTag)
            target.armed = true
            report["input_attempted"] = true
            let inputStarted = mach_absolute_time()
            report["input_started_ticks"] = String(inputStarted)
            down.post(tap: .cghidEventTap); up.post(tap: .cghidEventTap)
            let inputFinished = mach_absolute_time()
            report["input_status"] = "submitted"; report["input_finished_ticks"] = String(inputFinished)
            let deadline = Date().addingTimeInterval(3)
            while target.hits == 0, Date() < deadline { try Task.checkCancellation(); try await Task.sleep(nanoseconds: 10_000_000) }
            guard target.hits == 1 else { throw ProbeFailure("calibration target did not receive exactly one tagged click") }
            let frameDeadline = Date().addingTimeInterval(5)
            var threshold = inputStarted
            var after: CapturedFrame
            var newRGB: [Int]
            var blue: Bool
            repeat {
                after = try await collector.next(after: threshold, deadline: frameDeadline)
                guard after.image.width == before.image.width, after.image.height == before.image.height,
                      after.contentRect == before.contentRect, after.scaleFactor == before.scaleFactor, after.contentScale == before.contentScale else { throw ProbeFailure("capture geometry changed") }
                newRGB = try sampleRGB(after.image, at: samplePoint)
                blue = newRGB[2] > newRGB[0] + 50 && newRGB[2] > newRGB[1] + 30
                threshold = after.displayTime
            } while !blue
            try savePNG(after, to: directory.appendingPathComponent("after.png"))
            let green = oldRGB[1] > oldRGB[0] + 50 && oldRGB[1] > oldRGB[2] + 50
            report["target_event_observed"] = true; report["target_color_changed"] = green && blue
            report["before_rgb"] = oldRGB; report["after_rgb"] = newRGB
            report["after_display_ticks"] = String(after.displayTime)
            guard green && blue else { throw ProbeFailure("captured target colors do not match; inspect coordinate/color mapping") }
            report["passed"] = true
        } catch {
            report["error"] = String(describing: error)
            report["cancelled"] = error is CancellationError
        }
        target.armed = false
        if let capture = stream {
            do { try await capture.stopCapture() } catch { report["stop_error"] = String(describing: error); report["passed"] = false }
        }
        do {
            let data = try JSONSerialization.data(withJSONObject: report, options: [.prettyPrinted, .sortedKeys])
            let path = directory.appendingPathComponent("report.json")
            try data.write(to: path, options: .atomic)
            try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: path.path)
            status.stringValue = "\(report["passed"] as? Bool == true ? "PASS" : "NOT PASSED")\n\(report["error"] as? String ?? "Report saved")\n\(directory.path)"
        } catch { status.stringValue = "报告保存失败，不能判定通过：\(error)" }
    }
}

@main
struct ComputerSpikeMain {
    @MainActor
    static func main() {
        let application = NSApplication.shared
        application.setActivationPolicy(.regular)
        let delegate = SpikeApp()
        application.delegate = delegate
        // NSApplication's delegate is weak; retain it throughout the event loop.
        withExtendedLifetime(delegate) {
            application.run()
        }
    }
}
