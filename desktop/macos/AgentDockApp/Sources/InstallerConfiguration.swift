import Foundation

enum TunnelMode: String, CaseIterable {
    case local = "none"
    case quick
    case named

    var title: String {
        switch self {
        case .local: return "仅本机使用"
        case .quick: return "临时公网访问"
        case .named: return "使用自己的 Cloudflare 域名"
        }
    }

    var detail: String {
        switch self {
        case .local:
            return "仅允许这台电脑访问，不启用 Cloudflare 公网访问；或者自行配置内网穿透或反向代理。"
        case .quick:
            return "通过 Cloudflare 自动生成临时公网地址，无需配置域名。适合临时访问或测试，地址可能会变化。"
        case .named:
            return "通过 Cloudflare Tunnel 使用自己的 HTTPS 域名，完成配置后即可获得稳定的公网地址。"
        }
    }
}

struct InstallRequest {
    let mode: TunnelMode
    let serverURL: String
    let tunnelToken: String

    init(mode: TunnelMode, serverURL: String, tunnelToken: String) {
        self.mode = mode
        self.serverURL = serverURL
        self.tunnelToken = tunnelToken
    }

    func validatedServerURL() throws -> String? {
        guard mode == .named else { return nil }
        let candidate = serverURL.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !candidate.isEmpty else {
            throw ValidationError("请填写固定 HTTPS 公网地址。")
        }
        guard var components = URLComponents(string: candidate) else {
            throw ValidationError("公网地址格式无效。")
        }
        guard components.scheme?.lowercased() == "https" else {
            throw ValidationError("公网地址必须使用 https://。")
        }
        guard let host = components.host?.lowercased(), !host.isEmpty else {
            throw ValidationError("公网地址缺少有效域名。")
        }
        guard host != "localhost", host.contains("."), !isIPAddress(host) else {
            throw ValidationError("公网地址必须填写域名，不能使用 localhost 或 IP。")
        }
        guard components.user == nil,
              components.password == nil,
              components.port == nil,
              components.query == nil,
              components.fragment == nil else {
            throw ValidationError("公网地址只能填写 HTTPS Origin，不能包含账号、端口、查询参数或片段。")
        }
        let path = components.percentEncodedPath
        guard path.isEmpty || path == "/" else {
            throw ValidationError("公网地址不能包含路径，请不要填写 /mcp。")
        }
        components.path = ""
        components.query = nil
        components.fragment = nil
        guard let normalized = components.string else {
            throw ValidationError("无法规范化公网地址。")
        }
        return normalized.hasSuffix("/") ? String(normalized.dropLast()) : normalized
    }

    func validatedTunnelToken() throws -> String? {
        guard mode == .named else { return nil }
        let token = tunnelToken.trimmingCharacters(in: .whitespacesAndNewlines)
        if token.isEmpty {
            // InstallerRunner 会从当前用户私密存储中复用此前保存的 Token；
            // 新安装或没有存储值时再给出明确错误。
            return nil
        }
        guard !token.contains("\n"), !token.contains("\r") else {
            throw ValidationError("Tunnel Token 必须是单行文本。")
        }
        return token
    }

    private func isIPAddress(_ host: String) -> Bool {
        let ipv4Parts = host.split(separator: ".", omittingEmptySubsequences: false)
        if ipv4Parts.count == 4 && ipv4Parts.allSatisfy({ part in
            guard let value = Int(part) else { return false }
            return value >= 0 && value <= 255
        }) {
            return true
        }
        return host.contains(":")
    }
}

struct ValidationError: LocalizedError {
    let message: String

    init(_ message: String) {
        self.message = message
    }

    var errorDescription: String? { message }
}
