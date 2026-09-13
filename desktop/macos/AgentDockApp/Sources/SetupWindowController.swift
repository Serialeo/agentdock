import AppKit
import Foundation

private enum QuickTunnelRefreshState {
    case idle
    case refreshing
    case failed
}

@MainActor
final class SetupWindowController: NSWindowController, NSWindowDelegate {
    private lazy var installer = InstallerRunner(service: service)
    private let publicEndpointChecker = PublicEndpointChecker()
    private let service: ServiceController
    private let menuLoginAgent: MenuLoginAgentController
    private let onChanged: () -> Void

    private let titleLabel = NSTextField(labelWithString: "AgentDock")
    private let subtitleLabel = NSTextField(labelWithString: "本机 MCP 服务与公网连接管理")
    private let stateLabel = NSTextField(labelWithString: "未安装")
    private let nexusStateLabel = NSTextField(labelWithString: "未配置")

    private let serviceSection = NSStackView()
    private let localAddress = NSTextField(labelWithString: "未安装")
    private let publicAddress = NSTextField(labelWithString: "未启用")
    private let publicCheckStatus = NSTextField(labelWithString: "")
    private let publicTestButton = NSButton(title: "测试", target: nil, action: nil)
    private let publicCopyButton = NSButton(title: "复制", target: nil, action: nil)
    private let authToken = NSTextField(labelWithString: "未生成")
    private let oauthPassword = NSTextField(labelWithString: "未生成")
    private let authReveal = NSButton(title: "显示", target: nil, action: nil)
    private let oauthReveal = NSButton(title: "显示", target: nil, action: nil)
    private let startStopButton = NSButton(title: "启动服务", target: nil, action: nil)
    private let restartButton = NSButton(title: "重新启动", target: nil, action: nil)
    private let updateButton = NSButton(title: "检查更新", target: nil, action: nil)

    private let publicMode = NSSegmentedControl(
        labels: ["仅本机", "临时地址", "固定域名"],
        trackingMode: .selectOne,
        target: nil,
        action: nil
    )
    private let modeDescription = NSTextField(wrappingLabelWithString: "")
    private let namedFields = NSStackView()
    private let serverURLField = NSTextField(string: "")
    private let tunnelTokenField = NSSecureTextField(string: "")

    private let progress = NSProgressIndicator()
    private let statusLabel = NSTextField(wrappingLabelWithString: "")
    private let applyButton = NSButton(title: "配置并启用", target: nil, action: nil)
    private let permissionsButton = NSButton(title: "权限检查…", target: nil, action: nil)
    private let advancedButton = NSButton(title: "高级设置…", target: nil, action: nil)
    private let logsButton = NSButton(title: "打开日志", target: nil, action: nil)

    private var currentStatus = ServiceStatus.missing
    private var initialMode: TunnelMode = .local
    private var initialServerURL = ""
    private var authTokenValue = ""
    private var oauthPasswordValue = ""
    private var authVisible = false
    private var oauthVisible = false
    private var isBusy = false
    private var quickTunnelRefreshState: QuickTunnelRefreshState = .idle

    private var migrationRequired: Bool {
        currentStatus.migrationRequired
    }
    private var displayedPublicMCPURL: URL?
    private var lastCheckedPublicMCPURL: URL?
    private var activePublicCheckURL: URL?
    private var publicCheckTask: Task<Void, Never>?

    private lazy var advancedSettings = AdvancedSettingsWindowController(
        service: service,
        menuLoginAgent: menuLoginAgent,
        onChanged: onChanged
    )
    private lazy var permissionsWindow = DesktopPermissionsWindowController()

    init(
        service: ServiceController,
        menuLoginAgent: MenuLoginAgentController,
        onChanged: @escaping () -> Void
    ) {
        self.service = service
        self.menuLoginAgent = menuLoginAgent
        self.onChanged = onChanged
        let window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 620, height: 590),
            styleMask: [.titled, .closable, .miniaturizable],
            backing: .buffered,
            defer: false
        )
        window.title = "AgentDock"
        window.isReleasedWhenClosed = false
        window.center()
        super.init(window: window)
        window.delegate = self
        configureUI()
    }

    required init?(coder: NSCoder) {
        fatalError("init(coder:) has not been implemented")
    }

    func present(status: ServiceStatus) {
        update(status: status)
        showWindow(nil)
        window?.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    func update(status: ServiceStatus) {
        currentStatus = status
        authVisible = false
        oauthVisible = false
        statusLabel.isHidden = true
        quickTunnelRefreshState = .idle
        cancelPublicCheck(clearLastResult: true)
        setBusy(false)

        if status.installed {
            titleLabel.stringValue = "AgentDock"
            subtitleLabel.stringValue = "本机 MCP 服务与公网连接管理"
            applyButton.title = "应用更改"
            advancedButton.isEnabled = true
            logsButton.isEnabled = true
            serviceSection.isHidden = false
            updateServiceSection(status)
            selectCurrentMode(configuration: status.configuration)
        } else {
            titleLabel.stringValue = "设置 AgentDock"
            subtitleLabel.stringValue = "配置本机服务并允许 AgentDock 在后台运行"
            stateLabel.stringValue = "● 尚未配置"
            stateLabel.textColor = .secondaryLabelColor
            applyButton.title = "配置并启用"
            applyButton.isEnabled = true
            advancedButton.isEnabled = false
            logsButton.isEnabled = false
            serviceSection.isHidden = true
            authTokenValue = ""
            oauthPasswordValue = ""
            select(mode: .local)
        }
        updateNexusState(status.nexusConnection)
        refreshCredentialFields()
        refreshChangeState()
        updateWindowHeight()
    }

    func refreshServiceStatus(_ status: ServiceStatus) {
        let installationChanged = currentStatus.installed != status.installed
        currentStatus = status
        if installationChanged {
            update(status: status)
            return
        }
        guard status.installed else { return }
        updateServiceSection(status)
        updateNexusState(status.nexusConnection)
        refreshCredentialFields()
    }

    func presentPermissions() {
        permissionsWindow.present()
    }

    func windowDidResignKey(_ notification: Notification) {
        authVisible = false
        oauthVisible = false
        refreshCredentialFields()
    }

    private func configureUI() {
        guard let contentView = window?.contentView else { return }

        titleLabel.font = .systemFont(ofSize: 25, weight: .semibold)
        subtitleLabel.textColor = .secondaryLabelColor
        stateLabel.alignment = .right
        stateLabel.font = .systemFont(ofSize: 13, weight: .medium)
        nexusStateLabel.alignment = .left
        nexusStateLabel.font = .systemFont(ofSize: 12, weight: .regular)

        let headerText = NSStackView(views: [titleLabel, subtitleLabel])
        headerText.orientation = .vertical
        headerText.alignment = .leading
        headerText.spacing = 3
        let header = NSStackView(views: [headerText, NSView(), stateLabel])
        header.orientation = .horizontal
        header.alignment = .centerY
        header.widthAnchor.constraint(equalToConstant: 564).isActive = true

        for field in [localAddress, publicAddress, authToken, oauthPassword] {
            field.lineBreakMode = .byTruncatingMiddle
            field.isSelectable = true
            field.font = .monospacedSystemFont(ofSize: 12, weight: .regular)
        }

        publicCheckStatus.font = .systemFont(ofSize: 11.5)
        publicCheckStatus.textColor = .secondaryLabelColor
        publicCheckStatus.isHidden = true
        publicCheckStatus.lineBreakMode = .byTruncatingTail
        publicCheckStatus.widthAnchor.constraint(equalToConstant: 450).isActive = true
        publicTestButton.bezelStyle = .inline
        publicTestButton.target = self
        publicTestButton.action = #selector(testPublicAddressPressed)
        publicCopyButton.bezelStyle = .inline
        publicCopyButton.target = self
        publicCopyButton.action = #selector(copyPublicAddress)

        authReveal.bezelStyle = .inline
        authReveal.target = self
        authReveal.action = #selector(toggleAuthToken)
        oauthReveal.bezelStyle = .inline
        oauthReveal.target = self
        oauthReveal.action = #selector(toggleOAuthPassword)

        serviceSection.orientation = .vertical
        serviceSection.alignment = .leading
        serviceSection.spacing = 8
        serviceSection.addArrangedSubview(sectionTitle("连接信息"))
        serviceSection.addArrangedSubview(valueRow(title: "本地 MCP", field: localAddress, actions: [copyButton(#selector(copyLocalAddress))]))
        serviceSection.addArrangedSubview(valueRow(title: "公网 MCP", field: publicAddress, actions: [publicTestButton, publicCopyButton]))
        serviceSection.addArrangedSubview(valueDetailRow(publicCheckStatus))
        serviceSection.addArrangedSubview(valueRow(title: "Nexus", field: nexusStateLabel, actions: []))
        serviceSection.addArrangedSubview(valueRow(title: "Bearer Token", field: authToken, actions: [authReveal, copyButton(#selector(copyAuthToken))]))
        serviceSection.addArrangedSubview(valueRow(title: "OAuth 密码", field: oauthPassword, actions: [oauthReveal, copyButton(#selector(copyOAuthPassword))]))

        startStopButton.target = self
        startStopButton.action = #selector(startStopPressed)
        restartButton.target = self
        restartButton.action = #selector(restartPressed)
        updateButton.target = self
        updateButton.action = #selector(updatePressed)
        let serviceActions = NSStackView(views: [startStopButton, restartButton, updateButton])
        serviceActions.orientation = .horizontal
        serviceActions.spacing = 8
        serviceSection.addArrangedSubview(serviceActions)

        publicMode.target = self
        publicMode.action = #selector(modeChanged)
        publicMode.segmentStyle = .rounded
        publicMode.widthAnchor.constraint(equalToConstant: 360).isActive = true
        modeDescription.textColor = .secondaryLabelColor
        modeDescription.font = .systemFont(ofSize: 12)
        modeDescription.widthAnchor.constraint(equalToConstant: 564).isActive = true

        serverURLField.placeholderString = "https://mini.example.com"
        tunnelTokenField.placeholderString = "粘贴 Cloudflare Tunnel Token"
        serverURLField.widthAnchor.constraint(equalToConstant: 430).isActive = true
        tunnelTokenField.widthAnchor.constraint(equalToConstant: 430).isActive = true
        serverURLField.target = self
        serverURLField.action = #selector(configurationEdited)
        tunnelTokenField.target = self
        tunnelTokenField.action = #selector(configurationEdited)

        namedFields.orientation = .vertical
        namedFields.alignment = .leading
        namedFields.spacing = 7
        namedFields.addArrangedSubview(formRow(title: "公网地址", control: serverURLField))
        namedFields.addArrangedSubview(formRow(title: "Tunnel Token", control: tunnelTokenField))

        progress.style = .spinning
        progress.controlSize = .small
        progress.isDisplayedWhenStopped = false
        statusLabel.textColor = .secondaryLabelColor
        statusLabel.setContentHuggingPriority(.defaultLow, for: .horizontal)
        statusLabel.isHidden = true

        logsButton.bezelStyle = .inline
        logsButton.target = self
        logsButton.action = #selector(openLogsPressed)
        permissionsButton.bezelStyle = .inline
        permissionsButton.target = self
        permissionsButton.action = #selector(openPermissionsPressed)
        advancedButton.bezelStyle = .inline
        advancedButton.target = self
        advancedButton.action = #selector(openAdvancedPressed)
        applyButton.bezelStyle = .rounded
        applyButton.keyEquivalent = "\r"
        applyButton.target = self
        applyButton.action = #selector(applyPressed)

        let footer = NSStackView(views: [logsButton, permissionsButton, advancedButton, progress, statusLabel, NSView(), applyButton])
        footer.orientation = .horizontal
        footer.alignment = .centerY
        footer.spacing = 10
        footer.widthAnchor.constraint(equalToConstant: 564).isActive = true

        let root = NSStackView(views: [
            header,
            separator(),
            serviceSection,
            separator(),
            sectionTitle("公网访问"),
            publicMode,
            modeDescription,
            namedFields,
            separator(),
            footer,
        ])
        root.orientation = .vertical
        root.alignment = .leading
        root.spacing = 12
        root.translatesAutoresizingMaskIntoConstraints = false
        contentView.addSubview(root)

        NSLayoutConstraint.activate([
            root.leadingAnchor.constraint(equalTo: contentView.leadingAnchor, constant: 28),
            root.trailingAnchor.constraint(equalTo: contentView.trailingAnchor, constant: -28),
            root.topAnchor.constraint(equalTo: contentView.topAnchor, constant: 24),
            root.bottomAnchor.constraint(lessThanOrEqualTo: contentView.bottomAnchor, constant: -22),
        ])
    }

    private func updateServiceSection(_ status: ServiceStatus) {
        if migrationRequired {
            stateLabel.stringValue = "● 需要迁移"
            stateLabel.textColor = .systemOrange
        } else if status.healthy {
            if AppVersion.matchesHealthVersion(status.version) {
                stateLabel.stringValue = "● 运行正常 · \(AppVersion.current)"
                stateLabel.textColor = .systemGreen
            } else {
                stateLabel.stringValue = "● 版本异常 · AgentDock \(AppVersion.current) · Core \(AppVersion.display(status.version))"
                stateLabel.textColor = .systemRed
            }
        } else if status.requiresApproval {
            stateLabel.stringValue = "● 需要允许后台运行"
            stateLabel.textColor = .systemOrange
        } else if status.loaded {
            stateLabel.stringValue = "● 服务异常"
            stateLabel.textColor = .systemRed
        } else {
            stateLabel.stringValue = "● 已停止"
            stateLabel.textColor = .secondaryLabelColor
        }

        let configuration = status.configuration
        localAddress.stringValue = configuration?.localMCPURL?.absoluteString ?? "配置不可用"
        renderPublicAddress(configuration?.publicMCPURL, automaticallyCheck: true)
        authTokenValue = configuration?.authToken ?? ""
        oauthPasswordValue = configuration?.oauthPassword ?? ""
        startStopButton.title = migrationRequired
            ? "等待迁移"
            : (status.requiresApproval ? "打开后台设置" : (status.loaded ? "停用服务" : "启用服务"))
        if !isBusy {
            startStopButton.isEnabled = status.installed && !migrationRequired
            restartButton.isEnabled = status.installed && !status.requiresApproval && !migrationRequired
            updateButton.isEnabled = status.installed && !migrationRequired
        }
    }

    private func updateNexusState(_ state: NexusConnectionState) {
        switch state {
        case .connected:
            nexusStateLabel.stringValue = "已连接"
        case .disconnected:
            nexusStateLabel.stringValue = "未连接"
        case .unconfigured:
            nexusStateLabel.stringValue = "未配置"
        case .configurationError:
            nexusStateLabel.stringValue = "配置异常"
        }
    }

    private func renderPublicAddress(_ publicMCPURL: URL?, automaticallyCheck: Bool) {
        switch quickTunnelRefreshState {
        case .refreshing:
            cancelPublicCheck(clearLastResult: true)
            displayedPublicMCPURL = nil
            publicAddress.stringValue = "正在生成新地址…"
            publicCheckStatus.stringValue = "旧地址已隐藏，等待新的临时公网地址"
            publicCheckStatus.textColor = .secondaryLabelColor
            publicCheckStatus.isHidden = false
            refreshPublicActions()
        case .failed:
            cancelPublicCheck(clearLastResult: true)
            displayedPublicMCPURL = nil
            publicAddress.stringValue = "未生成新地址"
            publicCheckStatus.stringValue = "刷新失败，旧地址未作为新地址显示"
            publicCheckStatus.textColor = .systemRed
            publicCheckStatus.isHidden = false
            refreshPublicActions()
        case .idle:
            setDisplayedPublicMCPURL(publicMCPURL, automaticallyCheck: automaticallyCheck)
        }
    }

    private func setDisplayedPublicMCPURL(_ publicMCPURL: URL?, automaticallyCheck: Bool) {
        let addressChanged = displayedPublicMCPURL != publicMCPURL
        if addressChanged {
            cancelPublicCheck(clearLastResult: true)
        }
        displayedPublicMCPURL = publicMCPURL
        publicAddress.stringValue = publicMCPURL?.absoluteString ?? "未启用"

        guard let publicMCPURL else {
            publicCheckStatus.stringValue = ""
            publicCheckStatus.isHidden = true
            refreshPublicActions()
            return
        }

        refreshPublicActions()
        if automaticallyCheck,
           activePublicCheckURL != publicMCPURL,
           lastCheckedPublicMCPURL != publicMCPURL {
            beginPublicCheck(publicMCPURL, automatic: true)
        }
    }

    private func beginQuickTunnelRefresh() {
        quickTunnelRefreshState = .refreshing
        renderPublicAddress(nil, automaticallyCheck: false)
    }

    private func markQuickTunnelRefreshFailed() {
        quickTunnelRefreshState = .failed
        renderPublicAddress(nil, automaticallyCheck: false)
    }

    private func beginPublicCheck(_ publicMCPURL: URL, automatic: Bool) {
        guard quickTunnelRefreshState == .idle else { return }
        if automatic, lastCheckedPublicMCPURL == publicMCPURL { return }

        cancelPublicCheck(clearLastResult: !automatic)
        activePublicCheckURL = publicMCPURL
        publicCheckStatus.stringValue = "正在检测公网访问…"
        publicCheckStatus.textColor = .secondaryLabelColor
        publicCheckStatus.isHidden = false
        refreshPublicActions()

        publicCheckTask = Task { [weak self] in
            guard let self else { return }
            let maximumAttempts = automatic ? 3 : 1
            var finalResult: PublicEndpointCheckResult?

            for attempt in 1...maximumAttempts {
                if Task.isCancelled { return }
                if maximumAttempts > 1 {
                    publicCheckStatus.stringValue = "正在检测公网访问（\(attempt)/\(maximumAttempts)）…"
                }
                let result = await publicEndpointChecker.check(publicMCPURL: publicMCPURL)
                finalResult = result
                if result.isReachable || attempt == maximumAttempts { break }
                try? await Task.sleep(nanoseconds: 1_000_000_000)
            }

            guard !Task.isCancelled, let finalResult else { return }
            finishPublicCheck(finalResult, for: publicMCPURL)
        }
    }

    private func finishPublicCheck(_ result: PublicEndpointCheckResult, for publicMCPURL: URL) {
        guard quickTunnelRefreshState == .idle,
              activePublicCheckURL == publicMCPURL,
              displayedPublicMCPURL == publicMCPURL else {
            return
        }

        activePublicCheckURL = nil
        publicCheckTask = nil
        lastCheckedPublicMCPURL = publicMCPURL
        let latency = result.latencyMilliseconds.map { " · \($0) ms" } ?? ""
        publicCheckStatus.stringValue = "● \(result.message)\(latency)"
        publicCheckStatus.textColor = result.isReachable ? .systemGreen : .systemRed
        publicCheckStatus.isHidden = false
        refreshPublicActions()
    }

    private func cancelPublicCheck(clearLastResult: Bool) {
        publicCheckTask?.cancel()
        publicCheckTask = nil
        activePublicCheckURL = nil
        if clearLastResult {
            lastCheckedPublicMCPURL = nil
        }
    }

    private func refreshPublicActions() {
        let hasAddress = quickTunnelRefreshState == .idle && displayedPublicMCPURL != nil
        publicCopyButton.isEnabled = hasAddress
        publicTestButton.title = activePublicCheckURL == nil ? "测试" : "检测中"
        publicTestButton.isEnabled = hasAddress && activePublicCheckURL == nil && !isBusy
    }

    private func selectCurrentMode(configuration: ServiceConfiguration?) {
        guard let publicURL = configuration?.publicURL, !publicURL.isEmpty else {
            initialMode = .local
            initialServerURL = ""
            select(mode: .local)
            return
        }
        if publicURL.contains(".trycloudflare.com") {
            initialMode = .quick
            initialServerURL = ""
            select(mode: .quick)
        } else {
            initialMode = .named
            initialServerURL = publicURL
            serverURLField.stringValue = publicURL
            select(mode: .named)
        }
        tunnelTokenField.stringValue = ""
    }

    private func select(mode: TunnelMode) {
        publicMode.selectedSegment = segment(for: mode)
        modeDescription.stringValue = mode.detail
        namedFields.isHidden = mode != .named
        if mode == .named, currentStatus.installed, initialMode == .named {
            tunnelTokenField.placeholderString = "留空表示保留现有 Tunnel Token"
        } else {
            tunnelTokenField.placeholderString = "粘贴 Cloudflare Tunnel Token"
        }
        updateWindowHeight()
    }

    private func segment(for mode: TunnelMode) -> Int {
        switch mode {
        case .local: return 0
        case .quick: return 1
        case .named: return 2
        }
    }

    private var selectedMode: TunnelMode {
        switch publicMode.selectedSegment {
        case 1: return .quick
        case 2: return .named
        default: return .local
        }
    }

    @objc private func modeChanged() {
        select(mode: selectedMode)
        refreshChangeState()
    }

    @objc private func configurationEdited() { refreshChangeState() }

    @objc private func applyPressed() {
        let request = InstallRequest(
            mode: selectedMode,
            serverURL: serverURLField.stringValue,
            tunnelToken: tunnelTokenField.stringValue
        )
        do {
            _ = try request.validatedServerURL()
            _ = try request.validatedTunnelToken()
        } catch {
            showStatus(error.localizedDescription, isError: true)
            return
        }

        let refreshingQuickTunnel = currentStatus.installed && initialMode == .quick && selectedMode == .quick
        if refreshingQuickTunnel {
            // 生成过程中立即隐藏旧地址，避免用户把旧地址误认为本次生成结果。
            beginQuickTunnelRefresh()
        }
        setBusy(true)
        showStatus(
            refreshingQuickTunnel ? "正在生成新的临时公网地址…" : "正在校验并应用 AgentDock 配置…",
            isError: false
        )
        Task {
            do {
                let result = try await installer.run(request: request)
                let resultPublicMCPURL: URL?
                if result.publicMCPURL.isEmpty {
                    resultPublicMCPURL = nil
                } else if let parsedURL = URL(string: result.publicMCPURL) {
                    resultPublicMCPURL = parsedURL
                } else {
                    throw ValidationError("安装器返回的公网 MCP 地址格式无效。")
                }

                authTokenValue = result.authToken
                oauthPasswordValue = result.oauthPassword
                localAddress.stringValue = result.localMCPURL
                quickTunnelRefreshState = .idle
                setDisplayedPublicMCPURL(resultPublicMCPURL, automaticallyCheck: true)
                tunnelTokenField.stringValue = ""
                authVisible = false
                oauthVisible = false
                refreshCredentialFields()
                showStatus(
                    refreshingQuickTunnel
                        ? "新的临时公网地址已生成，正在自动检测公网访问。"
                        : "AgentDock \(result.version) 已配置并正常运行。",
                    isError: false
                )
                setBusy(false)
                initialMode = selectedMode
                initialServerURL = selectedMode == .named ? (try? request.validatedServerURL()) ?? "" : ""
                refreshChangeState()
                onChanged()
            } catch {
                if refreshingQuickTunnel {
                    markQuickTunnelRefreshFailed()
                }
                setBusy(false)
                showStatus(error.localizedDescription, isError: true)
            }
        }
    }

    @objc private func startStopPressed() {
        if currentStatus.requiresApproval {
            service.openBackgroundItemsSettings()
            return
        }
        performServiceAction(currentStatus.loaded ? "停用" : "启用") {
            if self.currentStatus.loaded { try await self.service.stop() }
            else { try await self.service.start() }
        }
    }

    @objc private func restartPressed() {
        performServiceAction("重启") { try await self.service.restart() }
    }

    @objc private func updatePressed() {
        setBusy(true)
        showStatus("正在检查并安装更新…", isError: false)
        Task {
            do {
                let output = try await service.update()
                setBusy(false)
                showStatus(output.isEmpty ? "更新已完成。" : output, isError: false)
                onChanged()
            } catch {
                setBusy(false)
                showStatus(error.localizedDescription, isError: true)
            }
        }
    }

    private func performServiceAction(_ action: String, operation: @escaping () async throws -> Void) {
        setBusy(true)
        showStatus("正在\(action) AgentDock…", isError: false)
        Task {
            do {
                try await operation()
                setBusy(false)
                showStatus("AgentDock \(action)完成。", isError: false)
                onChanged()
            } catch {
                setBusy(false)
                showStatus(error.localizedDescription, isError: true)
            }
        }
    }

    @objc private func openPermissionsPressed() { permissionsWindow.present() }
    @objc private func openAdvancedPressed() { advancedSettings.present(status: currentStatus) }
    @objc private func openLogsPressed() { service.openLogs() }

    @objc private func testPublicAddressPressed() {
        guard let displayedPublicMCPURL else { return }
        beginPublicCheck(displayedPublicMCPURL, automatic: false)
    }

    @objc private func copyLocalAddress(_ sender: NSButton) { copy(localAddress.stringValue, button: sender) }
    @objc private func copyPublicAddress(_ sender: NSButton) {
        copy(displayedPublicMCPURL?.absoluteString ?? "", button: sender)
    }
    @objc private func copyAuthToken(_ sender: NSButton) { copy(authTokenValue, button: sender) }
    @objc private func copyOAuthPassword(_ sender: NSButton) { copy(oauthPasswordValue, button: sender) }

    @objc private func toggleAuthToken() {
        authVisible.toggle()
        refreshCredentialFields()
    }

    @objc private func toggleOAuthPassword() {
        oauthVisible.toggle()
        refreshCredentialFields()
    }

    private func refreshCredentialFields() {
        authToken.stringValue = displayedSecret(authTokenValue, visible: authVisible, empty: "未生成")
        oauthPassword.stringValue = displayedSecret(oauthPasswordValue, visible: oauthVisible, empty: "未启用")
        authReveal.title = authVisible ? "隐藏" : "显示"
        oauthReveal.title = oauthVisible ? "隐藏" : "显示"
    }

    private func displayedSecret(_ value: String, visible: Bool, empty: String) -> String {
        guard !value.isEmpty else { return empty }
        return visible ? value : String(repeating: "•", count: min(max(value.count, 12), 24))
    }

    private func copy(_ value: String, button: NSButton) {
        guard !value.isEmpty else { return }
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(value, forType: .string)
        let original = button.title
        button.title = "已复制"
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.2) {
            button.title = original
        }
    }

    private func refreshChangeState() {
        guard currentStatus.installed else {
            applyButton.title = "配置并启用"
            applyButton.isEnabled = !isBusy
            return
        }

        if migrationRequired {
            applyButton.title = "迁移并启用"
            applyButton.isEnabled = !isBusy
            return
        }

        let refreshingQuickTunnel = initialMode == .quick && selectedMode == .quick
        applyButton.title = refreshingQuickTunnel ? "重新生成临时地址" : "应用更改"

        let serverChanged = selectedMode == .named
            && serverURLField.stringValue.trimmingCharacters(in: .whitespacesAndNewlines).trimmingCharacters(in: CharacterSet(charactersIn: "/"))
                != initialServerURL.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        let changed = refreshingQuickTunnel
            || selectedMode != initialMode
            || serverChanged
            || !tunnelTokenField.stringValue.isEmpty
        applyButton.isEnabled = changed && !isBusy
    }

    private func setBusy(_ busy: Bool) {
        isBusy = busy
        for control in [publicMode, serverURLField, tunnelTokenField, startStopButton, restartButton, updateButton, advancedButton] {
            control.isEnabled = !busy && (control !== advancedButton || currentStatus.installed)
        }
        if !busy && migrationRequired {
            startStopButton.isEnabled = false
            restartButton.isEnabled = false
            updateButton.isEnabled = false
        }
        logsButton.isEnabled = !busy && currentStatus.installed
        if busy {
            applyButton.isEnabled = false
            progress.startAnimation(nil)
        } else {
            progress.stopAnimation(nil)
            refreshChangeState()
        }
        refreshPublicActions()
    }

    private func showStatus(_ message: String, isError: Bool) {
        statusLabel.stringValue = message
        statusLabel.textColor = isError ? .systemRed : .secondaryLabelColor
        statusLabel.isHidden = message.isEmpty
    }

    private func updateWindowHeight() {
        let installedHeight: CGFloat = selectedMode == .named ? 675 : 580
        let installHeight: CGFloat = selectedMode == .named ? 455 : 360
        let target = currentStatus.installed ? installedHeight : installHeight
        guard let window else { return }
        var frame = window.frame
        let delta = target - frame.height
        frame.origin.y -= delta
        frame.size.height = target
        window.setFrame(frame, display: true, animate: window.isVisible)
    }

    private func sectionTitle(_ title: String) -> NSTextField {
        let label = NSTextField(labelWithString: title)
        label.font = .systemFont(ofSize: 14, weight: .semibold)
        return label
    }

    private func separator() -> NSBox {
        let box = NSBox()
        box.boxType = .separator
        box.widthAnchor.constraint(equalToConstant: 564).isActive = true
        return box
    }

    private func formRow(title: String, control: NSView) -> NSView {
        let label = NSTextField(labelWithString: title)
        label.textColor = .secondaryLabelColor
        label.widthAnchor.constraint(equalToConstant: 96).isActive = true
        let row = NSStackView(views: [label, control])
        row.orientation = .horizontal
        row.alignment = .centerY
        row.spacing = 10
        return row
    }

    private func valueDetailRow(_ detail: NSTextField) -> NSView {
        let spacer = NSView()
        spacer.widthAnchor.constraint(equalToConstant: 102).isActive = true
        let row = NSStackView(views: [spacer, detail])
        row.orientation = .horizontal
        row.alignment = .centerY
        row.spacing = 0
        return row
    }

    private func valueRow(title: String, field: NSTextField, actions: [NSButton]) -> NSView {
        let label = NSTextField(labelWithString: title)
        label.textColor = .secondaryLabelColor
        label.widthAnchor.constraint(equalToConstant: 94).isActive = true
        field.widthAnchor.constraint(equalToConstant: actions.count > 1 ? 310 : 370).isActive = true
        let row = NSStackView(views: [label, field] + actions)
        row.orientation = .horizontal
        row.alignment = .centerY
        row.spacing = 8
        return row
    }

    private func copyButton(_ action: Selector) -> NSButton {
        let button = NSButton(title: "复制", target: self, action: action)
        button.bezelStyle = .inline
        return button
    }
}
