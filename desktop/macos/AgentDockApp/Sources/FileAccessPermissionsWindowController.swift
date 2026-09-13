import AppKit
import Foundation

@MainActor
final class FileAccessPermissionsWindowController: NSWindowController {
    private let contentStack = TopAlignedStackView()
    private let standardRows = NSStackView()
    private let selectedRows = NSStackView()
    private var standardChecks = FileAccessPermissionChecker.uncheckedStandardLocations()
    private var selectedURLs: [URL] = []

    init() {
        let window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 680, height: 570),
            styleMask: [.titled, .closable, .miniaturizable, .resizable],
            backing: .buffered,
            defer: false
        )
        window.title = "AgentDock 文件访问检查"
        window.isReleasedWhenClosed = false
        window.minSize = NSSize(width: 610, height: 500)
        window.center()
        super.init(window: window)
        configureUI()
    }

    required init?(coder: NSCoder) {
        fatalError("init(coder:) has not been implemented")
    }

    func present() {
        renderRows()
        showWindow(nil)
        window?.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    private func configureUI() {
        guard let contentView = window?.contentView else { return }

        let scrollView = NSScrollView()
        scrollView.hasVerticalScroller = true
        scrollView.autohidesScrollers = true
        scrollView.drawsBackground = false
        scrollView.translatesAutoresizingMaskIntoConstraints = false
        contentView.addSubview(scrollView)

        contentStack.orientation = .vertical
        contentStack.alignment = .leading
        contentStack.spacing = 12
        contentStack.edgeInsets = NSEdgeInsets(top: 22, left: 26, bottom: 22, right: 26)
        contentStack.translatesAutoresizingMaskIntoConstraints = false
        scrollView.documentView = contentStack

        let title = NSTextField(labelWithString: "文件访问")
        title.font = .systemFont(ofSize: 22, weight: .semibold)
        let intro = PermissionUI.detailLabel(
            "检查 AgentDock 是否可以访问桌面、文稿、下载和你选择的其他目录。"
        )
        intro.widthAnchor.constraint(equalToConstant: 600).isActive = true
        contentStack.addArrangedSubview(title)
        contentStack.addArrangedSubview(intro)
        contentStack.addArrangedSubview(PermissionUI.separator())

        standardRows.orientation = .vertical
        standardRows.alignment = .leading
        standardRows.spacing = 8
        contentStack.addArrangedSubview(standardRows)
        let checkStandardButton = NSButton(
            title: "检查标准目录",
            target: self,
            action: #selector(checkStandardDirectories)
        )
        checkStandardButton.bezelStyle = .rounded
        contentStack.addArrangedSubview(checkStandardButton)

        let selectedTitle = NSTextField(labelWithString: "其他目录")
        selectedTitle.font = .systemFont(ofSize: 15, weight: .semibold)
        contentStack.addArrangedSubview(PermissionUI.separator())
        contentStack.addArrangedSubview(selectedTitle)
        selectedRows.orientation = .vertical
        selectedRows.alignment = .leading
        selectedRows.spacing = 8
        contentStack.addArrangedSubview(selectedRows)

        let selectButton = NSButton(title: "选择目录检查…", target: self, action: #selector(selectDirectory))
        selectButton.bezelStyle = .rounded
        let filesSettingsButton = NSButton(title: "打开“文件与文件夹”设置", target: self, action: #selector(openFilesSettings))
        filesSettingsButton.bezelStyle = .rounded
        let selectedActions = NSStackView(views: [selectButton, filesSettingsButton])
        selectedActions.orientation = .horizontal
        selectedActions.spacing = 8
        contentStack.addArrangedSubview(selectedActions)

        contentStack.addArrangedSubview(PermissionUI.separator())
        let fullDiskTitle = NSTextField(labelWithString: "完全磁盘访问")
        fullDiskTitle.font = .systemFont(ofSize: 15, weight: .semibold)
        let fullDiskDetail = PermissionUI.detailLabel(
            "用于访问其他应用的受保护数据。"
        )
        fullDiskDetail.widthAnchor.constraint(equalToConstant: 600).isActive = true
        let fullDiskButton = NSButton(title: "打开完全磁盘访问设置", target: self, action: #selector(openFullDiskSettings))
        fullDiskButton.bezelStyle = .rounded
        contentStack.addArrangedSubview(fullDiskTitle)
        contentStack.addArrangedSubview(fullDiskDetail)
        contentStack.addArrangedSubview(fullDiskButton)

        NSLayoutConstraint.activate([
            scrollView.leadingAnchor.constraint(equalTo: contentView.leadingAnchor),
            scrollView.trailingAnchor.constraint(equalTo: contentView.trailingAnchor),
            scrollView.topAnchor.constraint(equalTo: contentView.topAnchor),
            scrollView.bottomAnchor.constraint(equalTo: contentView.bottomAnchor),
            contentStack.widthAnchor.constraint(equalTo: scrollView.contentView.widthAnchor),
        ])
    }

    private func renderRows() {
        replaceRows(in: standardRows, with: standardChecks)
        let selectedChecks = selectedURLs.map {
            FileAccessPermissionChecker.check(title: $0.lastPathComponent.isEmpty ? $0.path : $0.lastPathComponent, url: $0)
        }
        replaceRows(in: selectedRows, with: selectedChecks, emptyText: "尚未选择其他目录。")
    }

    private func replaceRows(in stack: NSStackView, with checks: [FileAccessCheck], emptyText: String? = nil) {
        for view in stack.arrangedSubviews {
            stack.removeArrangedSubview(view)
            view.removeFromSuperview()
        }
        if checks.isEmpty, let emptyText {
            stack.addArrangedSubview(PermissionUI.detailLabel(emptyText))
            return
        }
        for check in checks {
            stack.addArrangedSubview(makeFileRow(check))
        }
    }

    private func makeFileRow(_ check: FileAccessCheck) -> NSView {
        let title = NSTextField(labelWithString: check.title)
        title.font = .systemFont(ofSize: 13, weight: .medium)
        let path = PermissionUI.detailLabel(check.url.path)
        path.lineBreakMode = .byTruncatingMiddle
        path.widthAnchor.constraint(equalToConstant: 430).isActive = true
        let text = NSStackView(views: [title, path])
        text.orientation = .vertical
        text.alignment = .leading
        text.spacing = 2

        let status = PermissionUI.statusLabel(check.state.title)
        status.widthAnchor.constraint(equalToConstant: 88).isActive = true
        switch check.state {
        case .accessible:
            PermissionUI.applyColor(to: status, granted: true)
        case .denied:
            PermissionUI.applyColor(to: status, granted: false)
        case .notChecked:
            PermissionUI.applyColor(to: status, granted: nil, attention: true)
        case .missing, .unavailable:
            PermissionUI.applyColor(to: status, granted: nil)
        }

        let row = NSStackView(views: [text, NSView(), status])
        row.orientation = .horizontal
        row.alignment = .centerY
        row.widthAnchor.constraint(equalToConstant: 600).isActive = true
        return row
    }

    @objc private func selectDirectory() {
        let panel = NSOpenPanel()
        panel.title = "选择要检查的目录"
        panel.canChooseFiles = false
        panel.canChooseDirectories = true
        panel.allowsMultipleSelection = true
        panel.canCreateDirectories = false
        guard panel.runModal() == .OK else { return }
        for url in panel.urls where !selectedURLs.contains(url) {
            selectedURLs.append(url)
        }
        renderRows()
    }

    @objc private func openFilesSettings() {
        DesktopPermissionChecker.openFilesAndFoldersSettings()
    }

    @objc private func openFullDiskSettings() {
        DesktopPermissionChecker.openFullDiskAccessSettings()
    }

    @objc private func checkStandardDirectories() {
        standardChecks = FileAccessPermissionChecker.standardLocations()
        renderRows()
    }
}
