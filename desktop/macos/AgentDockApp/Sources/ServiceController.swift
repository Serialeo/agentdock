import AppKit
import Foundation
import ServiceManagement

struct HealthPayload: Decodable {
    let ok: Bool
    let version: String
}

struct DesktopServiceStatusPayload: Decodable {
    let nexusConnected: Bool

    private enum CodingKeys: String, CodingKey {
        case nexusConnected = "nexus_connected"
    }
}

enum NexusConnectionState: Equatable {
    case unconfigured
    case connected
    case disconnected
    case configurationError

    static func resolve(device: NexusDeviceStatus, connected: Bool) -> NexusConnectionState {
        if device.error != nil {
            return .configurationError
        }
        guard device.paired else {
            return .unconfigured
        }
        return connected ? .connected : .disconnected
    }
}

struct DesktopUpdateCheck: Decodable {
    let updateAvailable: Bool
    let message: String

    private enum CodingKeys: String, CodingKey {
        case updateAvailable = "update_available"
        case message
    }

    static func decode(_ output: String) throws -> DesktopUpdateCheck {
        guard let data = output.data(using: .utf8) else {
            throw ValidationError("无法读取 AgentDock 更新检查结果。")
        }
        do {
            return try JSONDecoder().decode(DesktopUpdateCheck.self, from: data)
        } catch {
            throw ValidationError("无法解析 AgentDock 更新检查结果。")
        }
    }
}

struct ServiceStatus {
    let installed: Bool
    let loaded: Bool
    let healthy: Bool
    let version: String?
    let configuration: ServiceConfiguration?
    let autostartEnabled: Bool
    let requiresApproval: Bool
    let migrationRequired: Bool
    let nexusConnection: NexusConnectionState

    static let missing = ServiceStatus(
        installed: false,
        loaded: false,
        healthy: false,
        version: nil,
        configuration: nil,
        autostartEnabled: false,
        requiresApproval: false,
        migrationRequired: false,
        nexusConnection: .unconfigured
    )
}

final class ServiceController: @unchecked Sendable {
    static let coreLabel = "com.uvwt.agentdock.core"
    static let tunnelLabel = "com.uvwt.agentdock.tunnel"
    static let corePlistName = "com.uvwt.agentdock.core.plist"
    static let tunnelPlistName = "com.uvwt.agentdock.tunnel.plist"

    let paths: AppPaths

    init(paths: AppPaths = AppPaths()) {
        self.paths = paths
    }

    func status() async -> ServiceStatus {
        let fileManager = FileManager.default
        let migrationRequired = LegacyDesktopRuntimeMigration.isPresent(paths: paths)
        let installed = fileManager.isExecutableFile(atPath: paths.binary.path)
            && fileManager.isExecutableFile(atPath: paths.cloudflared.path)
            && fileManager.fileExists(atPath: paths.coreSkillBundle.appendingPathComponent("manifest.json").path)
            && fileManager.fileExists(atPath: paths.environment.path)
        guard installed else { return .missing }

        let configuration = ServiceConfiguration.load(from: paths.environment)
        let nexusDevice = nexusDeviceStatus()
        let registration = coreService.status
        let requiresApproval = registration == .requiresApproval
        let enabled = registration == .enabled
        let registered = enabled || requiresApproval
        let loaded = enabled && isLoaded(label: Self.coreLabel)
        let nexusConnected = loaded && nexusDevice.paired ? await fetchNexusConnected() : false

        guard loaded, let healthURL = configuration?.healthURL else {
            return ServiceStatus(
                installed: true,
                loaded: loaded,
                healthy: false,
                version: nil,
                configuration: configuration,
                autostartEnabled: registered,
                requiresApproval: requiresApproval,
                migrationRequired: migrationRequired,
                nexusConnection: .resolve(device: nexusDevice, connected: nexusConnected)
            )
        }

        let health = await fetchHealth(url: healthURL)
        return ServiceStatus(
            installed: true,
            loaded: true,
            healthy: health?.ok == true,
            version: health?.version,
            configuration: configuration,
            autostartEnabled: registered,
            requiresApproval: requiresApproval,
            migrationRequired: migrationRequired,
            nexusConnection: .resolve(device: nexusDevice, connected: nexusConnected)
        )
    }

    func start() async throws {
        try registerCoreIfNeeded()
        guard let configuration = ServiceConfiguration.load(from: paths.environment),
              await waitForHealth(configuration: configuration) else {
            throw ValidationError("AgentDock 后台服务已启用，但健康检查没有通过。")
        }
    }

    func stop() async throws {
        try unregister(service: coreService, label: Self.coreLabel)
    }

    func restart() async throws {
        try reregister(service: coreService, label: Self.coreLabel, displayName: "AgentDock Core")
        guard let configuration = ServiceConfiguration.load(from: paths.environment),
              await waitForHealth(configuration: configuration) else {
            throw ValidationError("AgentDock Core 已重新注册，但健康检查没有通过。")
        }
    }

    func nexusDeviceStatus() -> NexusDeviceStatus {
        NexusDeviceStatus.load(from: paths.nexusDeviceIdentity)
    }

    func pairNexus(endpoint: String, pairingCode: String) async throws {
        let endpoint = endpoint.trimmingCharacters(in: .whitespacesAndNewlines)
        let pairingCode = pairingCode.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !endpoint.isEmpty, !pairingCode.isEmpty else {
            throw ValidationError("NexusDock 地址和一次性配对码不能为空。")
        }
        let result = try await runInBackground {
            try runProcess(
                executable: self.paths.binary.path,
                arguments: ["nexus", "pair", "--endpoint", endpoint, "--code", pairingCode]
            )
        }
        guard result.status == 0 else {
            throw ValidationError(commandError(result.output, action: "NexusDock 配对"))
        }
        try await restart()
    }

    func setAutostart(enabled: Bool) async throws {
        if enabled {
            try await start()
        } else {
            try await stop()
        }
    }

    func tunnelEnabled() -> Bool {
        let status = tunnelService.status
        return status == .enabled || status == .requiresApproval
    }

    func configuredTunnelMode() throws -> TunnelMode {
        guard FileManager.default.fileExists(atPath: paths.tunnelEnvironment.path) else {
            return .local
        }
        let environment = try ManagedEnvironment.load(from: paths.tunnelEnvironment)
        let rawMode = environment.values["AGENTDOCK_TUNNEL_MODE"]?
            .trimmingCharacters(in: .whitespacesAndNewlines)
            .lowercased() ?? ""
        return TunnelMode(rawValue: rawMode) ?? .local
    }

    func reconcileTunnelRegistrationFromConfiguration() async throws {
        // 旧结构仍存在时必须先走迁移事务，不能在旁边提前注册第二套 Tunnel。
        guard !LegacyDesktopRuntimeMigration.isPresent(paths: paths) else { return }

        switch try configuredTunnelMode() {
        case .local:
            try setTunnelEnabled(false)
        case .quick, .named:
            try setTunnelEnabled(true)
            if tunnelService.status == .enabled, !(await waitForTunnelProcess()) {
                // App Bundle 被原子替换后，macOS 偶尔仍把旧 SMAppService 注册显示为 enabled，
                // 但 launchd 保存的 Bundle 关联已经失效。此时单纯再次 register 会直接 no-op；
                // 必须完整注销并重新注册，效果等同于用户手动“仅本地 → 公网”但无需人工介入。
                NSLog("AgentDock Tunnel 注册显示 enabled 但进程未稳定，开始自动重新注册。")
                try restartTunnel()
                guard await waitForTunnelProcess() else {
                    throw ValidationError("AgentDock Tunnel 已重新注册，但后台进程没有稳定启动。")
                }
            }
        }
    }

    func setTunnelEnabled(_ enabled: Bool) throws {
        if enabled {
            try register(
                service: tunnelService,
                plistName: Self.tunnelPlistName,
                displayName: "AgentDock Tunnel"
            )
        } else {
            try unregister(service: tunnelService, label: Self.tunnelLabel)
        }
    }

    func restartTunnel() throws {
        try reregister(service: tunnelService, label: Self.tunnelLabel, displayName: "AgentDock Tunnel")
    }

    func restoreBackgroundServiceRegistrations(coreEnabled: Bool, tunnelEnabled: Bool) throws {
        if coreEnabled {
            try restoreRegistration(service: coreService, label: Self.coreLabel, displayName: "AgentDock Core")
        } else {
            try unregister(service: coreService, label: Self.coreLabel)
        }
        if tunnelEnabled {
            try restoreRegistration(service: tunnelService, label: Self.tunnelLabel, displayName: "AgentDock Tunnel")
        } else {
            try unregister(service: tunnelService, label: Self.tunnelLabel)
        }
    }

    func recoverBackgroundServicesAfterUpdate(coreEnabled: Bool, tunnelEnabled: Bool) async -> [String] {
        // App Bundle 刚替换后，SMAppService 的注册状态可能已经生效，但 launchd 真正拉起
        // Core/Tunnel 仍需要更长时间。先给系统一个正常传播窗口，再做一次有界自愈；
        // 自愈仍失败时只提示，不把已经完成 handoff 的 App 更新回滚掉。
        var warnings: [String] = []
        if tunnelEnabled,
           tunnelService.status == .enabled,
           !(await waitForTunnelProcess()) {
            warnings.append("AgentDock Tunnel 已恢复后台注册，但进程仍在启动。")
        }
        if coreEnabled,
           coreService.status == .enabled,
           let configuration = ServiceConfiguration.load(from: paths.environment),
           !(await waitForHealth(configuration: configuration, timeout: 10)) {
            // 实机更新后可能出现“SMAppService 显示 enabled，但 Core 进程没有真正拉起”的状态。
            // 控制面板“重启”之所以能恢复，是因为它会完整 unregister/register；这里复用同一路径，
            // 避免用户在每次 App 更新后手动点击重启。
            NSLog("AgentDock Core 注册显示 enabled 但健康检查未通过，开始自动重新注册。")
            do {
                try await restart()
            } catch {
                warnings.append("AgentDock Core 已恢复后台注册，但自动重启仍未通过健康检查：\(error.localizedDescription)")
            }
        }
        return warnings
    }

    func reregisterBackgroundServices(coreEnabled: Bool, tunnelEnabled: Bool) async throws -> [String] {
        try restoreBackgroundServiceRegistrations(coreEnabled: coreEnabled, tunnelEnabled: tunnelEnabled)
        return await recoverBackgroundServicesAfterUpdate(coreEnabled: coreEnabled, tunnelEnabled: tunnelEnabled)
    }

    func openBackgroundItemsSettings() {
        SMAppService.openSystemSettingsLoginItems()
    }

    func waitForTunnelProcess(timeout: TimeInterval = 10) async -> Bool {
        await withCheckedContinuation { continuation in
            DispatchQueue.global(qos: .userInitiated).async {
                continuation.resume(returning: self.waitForStableLaunchdProcess(
                    label: Self.tunnelLabel,
                    timeout: timeout
                ))
            }
        }
    }

    func isLoaded() -> Bool {
        isLoaded(label: Self.coreLabel)
    }

    func waitForHealth(configuration: ServiceConfiguration, timeout: TimeInterval = 30) async -> Bool {
        await withCheckedContinuation { continuation in
            DispatchQueue.global(qos: .userInitiated).async {
                continuation.resume(returning: self.waitForHealthSynchronously(configuration: configuration, timeout: timeout))
            }
        }
    }

    func update() async throws -> String {
        try validateServiceManagementReadiness()

        let check = try await runInBackground {
            let result = try runProcess(
                executable: self.paths.binary.path,
                arguments: ["update", "--check"],
                environment: ["AGENTDOCK_DESKTOP_APP_PATH": self.paths.appBundle.path]
            )
            guard result.status == 0 else {
                throw ValidationError(self.commandError(result.output, action: "检查更新"))
            }
            return try DesktopUpdateCheck.decode(result.output)
        }
        guard check.updateAvailable else {
            // 没有 pending update result 时这只能是上一次未完成流程留下的临时状态。
            DesktopUpdateServiceState.remove(at: paths.updateServiceState)
            return check.message
        }

        let currentStatus = await status()
        let serviceState = DesktopUpdateServiceState(
            coreEnabled: currentStatus.autostartEnabled,
            tunnelEnabled: tunnelEnabled()
        )
        try serviceState.write(to: paths.updateServiceState)

        let output: String
        do {
            try setTunnelEnabled(false)
            try await stop()
            output = try await runInBackground {
                let result = try runUpdateProcess(
                    executable: self.paths.binary.path,
                    arguments: ["update"],
                    environment: ["AGENTDOCK_DESKTOP_APP_PATH": self.paths.appBundle.path],
                    outputURL: self.paths.updateLog
                )
                guard result.status == 0 else {
                    throw ValidationError(self.commandError(result.output, action: "更新"))
                }
                return result.output.trimmingCharacters(in: .whitespacesAndNewlines)
            }
        } catch {
            let updateError = error
            do {
                let recoveryWarnings = try await reregisterBackgroundServices(
                    coreEnabled: serviceState.coreEnabled,
                    tunnelEnabled: serviceState.tunnelEnabled
                )
                if !recoveryWarnings.isEmpty {
                    NSLog("AgentDock 更新失败后后台服务仍在启动：%@", recoveryWarnings.joined(separator: "；"))
                }
                DesktopUpdateServiceState.remove(at: paths.updateServiceState)
            } catch {
                throw ValidationError("更新没有应用，而且后台服务恢复失败：\(updateError.localizedDescription)；\(error.localizedDescription)")
            }
            throw updateError
        }

        // 真正的 App 替换会终止旧 GUI，并由新版 App 根据 update-result.json 恢复服务。
        // 能执行到这里说明更新进程正常返回但没有完成 GUI handoff，因此旧 GUI 必须自己收尾。
        do {
            let recoveryWarnings = try await reregisterBackgroundServices(
                coreEnabled: serviceState.coreEnabled,
                tunnelEnabled: serviceState.tunnelEnabled
            )
            if !recoveryWarnings.isEmpty {
                NSLog("AgentDock 更新返回后后台服务仍在启动：%@", recoveryWarnings.joined(separator: "；"))
            }
            DesktopUpdateServiceState.remove(at: paths.updateServiceState)
        } catch {
            throw ValidationError("更新进程已经返回，但后台服务恢复失败：\(error.localizedDescription)")
        }
        return output
    }

    func openLogs() {
        try? FileManager.default.createDirectory(at: paths.logs, withIntermediateDirectories: true)
        NSWorkspace.shared.open(paths.logs)
    }

    func openConfiguration() {
        try? FileManager.default.createDirectory(at: paths.appSupport, withIntermediateDirectories: true)
        NSWorkspace.shared.open(paths.appSupport)
    }

    private var coreService: SMAppService {
        SMAppService.agent(plistName: Self.corePlistName)
    }

    private var tunnelService: SMAppService {
        SMAppService.agent(plistName: Self.tunnelPlistName)
    }

    private func registerCoreIfNeeded() throws {
        try register(
            service: coreService,
            plistName: Self.corePlistName,
            displayName: "AgentDock Core"
        )
    }

    private func register(service: SMAppService, plistName: String, displayName: String) throws {
        try validateServiceManagementReadiness()
        try validateBundledServiceDefinition(plistName: plistName, displayName: displayName)
        switch service.status {
        case .enabled:
            return
        case .requiresApproval:
            throw ValidationError("\(displayName) 已注册，但需要你在“系统设置 → 通用 → 登录项与扩展”中允许后台运行。")
        case .notRegistered, .notFound:
            // SMAppService 在服务首次 register 前可能返回 .notFound，即使 Bundle 内 plist
            // 已经存在。定义是否完整由上面的 Bundle 文件校验负责，不用 status 猜测。
            try service.register()
        @unknown default:
            throw ValidationError("无法确认 \(displayName) 的后台服务状态。")
        }
        if service.status == .requiresApproval {
            throw ValidationError("\(displayName) 需要你在“系统设置 → 通用 → 登录项与扩展”中允许后台运行。")
        }
        guard service.status == .enabled else {
            throw ValidationError("\(displayName) 注册完成，但系统没有将它标记为可运行。")
        }
    }

    private func unregister(service: SMAppService, label: String) throws {
        switch service.status {
        case .notRegistered, .notFound:
            return
        case .enabled, .requiresApproval:
            try service.unregister()
            guard waitUntilUnregistered(service: service, label: label, timeout: 5) else {
                throw ValidationError("后台服务 \(label) 注销后系统状态没有完成收敛。")
            }
        @unknown default:
            return
        }
    }

    private func reregister(service: SMAppService, label: String, displayName: String) throws {
        try unregister(service: service, label: label)
        let plistName = label == Self.coreLabel ? Self.corePlistName : Self.tunnelPlistName
        try register(service: service, plistName: plistName, displayName: displayName)
    }

    private func restoreRegistration(service: SMAppService, label: String, displayName: String) throws {
        try validateServiceManagementReadiness()
        let plistName = label == Self.coreLabel ? Self.corePlistName : Self.tunnelPlistName
        try validateBundledServiceDefinition(plistName: plistName, displayName: displayName)
        try unregister(service: service, label: label)
        try register(service: service, plistName: plistName, displayName: displayName)
    }

    private var serviceDomain: String { "gui/\(getuid())" }

    func validatePersistentAppLocation() throws {
        let path = paths.appBundle.resolvingSymlinksInPath().path
        if path == "/Volumes" || path.hasPrefix("/Volumes/") {
            throw ValidationError("请先把 AgentDock 拖到“应用程序”文件夹，再启用后台服务。")
        }
    }

    func validateServiceManagementReadiness() throws {
        try validatePersistentAppLocation()
        if LegacyDesktopRuntimeMigration.isPresent(paths: paths) {
            throw ValidationError("检测到旧版 AgentDock 后台结构，请先在主面板应用当前设置完成迁移。")
        }
    }

    func validateBundledServiceDefinition(plistName: String, displayName: String) throws {
        let plist = paths.appBundle
            .appendingPathComponent("Contents/Library/LaunchAgents", isDirectory: true)
            .appendingPathComponent(plistName)
        guard let values = try? plist.resourceValues(forKeys: [.isRegularFileKey, .isSymbolicLinkKey]),
              values.isRegularFile == true,
              values.isSymbolicLink != true else {
            throw ValidationError("AgentDock.app 缺少 \(displayName) 的后台服务定义，请重新安装应用。")
        }
    }

    private func isLoaded(label: String) -> Bool {
        (try? runProcess(
            executable: "/bin/launchctl",
            arguments: ["print", "\(serviceDomain)/\(label)"]
        ).status) == 0
    }

    private func launchdProcessID(label: String) -> Int? {
        guard let result = try? runProcess(
            executable: "/bin/launchctl",
            arguments: ["print", "\(serviceDomain)/\(label)"]
        ), result.status == 0 else { return nil }
        for rawLine in result.output.split(whereSeparator: \.isNewline) {
            let line = rawLine.trimmingCharacters(in: .whitespaces)
            guard line.hasPrefix("pid = "),
                  let pid = Int(line.dropFirst("pid = ".count)),
                  pid > 0 else { continue }
            return pid
        }
        return nil
    }

    private func waitForStableLaunchdProcess(label: String, timeout: TimeInterval) -> Bool {
        let deadline = Date().addingTimeInterval(timeout)
        var previousPID: Int?
        var stableChecks = 0
        while Date() < deadline {
            if let pid = launchdProcessID(label: label) {
                if pid == previousPID {
                    stableChecks += 1
                } else {
                    previousPID = pid
                    stableChecks = 1
                }
                if stableChecks >= 4 { return true }
            } else {
                previousPID = nil
                stableChecks = 0
            }
            Thread.sleep(forTimeInterval: 0.25)
        }
        return false
    }

    private func waitUntilUnregistered(service: SMAppService, label: String, timeout: TimeInterval) -> Bool {
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if !isLoaded(label: label), Self.isUnregistered(service.status) { return true }
            Thread.sleep(forTimeInterval: 0.1)
        }
        return !isLoaded(label: label) && Self.isUnregistered(service.status)
    }

    static func isUnregistered(_ status: SMAppService.Status) -> Bool {
        status == .notRegistered || status == .notFound
    }

    private func waitForHealthSynchronously(configuration: ServiceConfiguration, timeout: TimeInterval) -> Bool {
        guard let url = configuration.healthURL else { return false }
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            let semaphore = DispatchSemaphore(value: 0)
            var healthy = false
            var request = URLRequest(url: url)
            request.timeoutInterval = 1.5
            URLSession.shared.dataTask(with: request) { data, response, _ in
                defer { semaphore.signal() }
                guard (response as? HTTPURLResponse)?.statusCode == 200,
                      let data,
                      let payload = try? JSONDecoder().decode(HealthPayload.self, from: data) else { return }
                healthy = payload.ok
            }.resume()
            _ = semaphore.wait(timeout: .now() + 2)
            if healthy { return true }
            Thread.sleep(forTimeInterval: 0.5)
        }
        return false
    }

    private func fetchNexusConnected() async -> Bool {
        do {
            let result = try await runInBackground {
                try runProcess(
                    executable: self.paths.binary.path,
                    arguments: ["service", "status", "--runtime-root", self.paths.appSupport.path]
                )
            }
            guard result.status == 0,
                  let data = result.output.data(using: .utf8),
                  let payload = try? JSONDecoder().decode(DesktopServiceStatusPayload.self, from: data) else {
                return false
            }
            return payload.nexusConnected
        } catch {
            return false
        }
    }

    private func fetchHealth(url: URL) async -> HealthPayload? {
        do {
            var request = URLRequest(url: url)
            request.timeoutInterval = 2.5
            let (data, response) = try await URLSession.shared.data(for: request)
            guard (response as? HTTPURLResponse)?.statusCode == 200 else { return nil }
            return try JSONDecoder().decode(HealthPayload.self, from: data)
        } catch {
            return nil
        }
    }

    private func commandError(_ output: String, action: String) -> String {
        let message = output.trimmingCharacters(in: .whitespacesAndNewlines)
        return message.isEmpty ? "AgentDock \(action)失败。" : message
    }

    func runInBackground<T>(_ operation: @escaping () throws -> T) async throws -> T {
        try await withCheckedThrowingContinuation { continuation in
            DispatchQueue.global(qos: .userInitiated).async {
                do { continuation.resume(returning: try operation()) }
                catch { continuation.resume(throwing: error) }
            }
        }
    }
}
