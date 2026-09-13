import Foundation

struct TunnelTokenStore {
    private static let maximumTokenBytes = 16 * 1024

    let paths: AppPaths

    init(paths: AppPaths = AppPaths()) {
        self.paths = paths
    }

    func captureExistingTokenIfPresent() throws {
        guard let token = try activeTunnelToken() else { return }
        try persist(token)
    }

    func tokenForNamedTunnel(providedToken: String?) throws -> String {
        if let providedToken {
            return try validated(providedToken)
        }
        if let stored = try storedToken() {
            return stored
        }
        if let active = try activeTunnelToken() {
            try persist(active)
            return active
        }
        throw ValidationError("请填写 Cloudflare Tunnel Token；此前保存过 Token 时可以留空复用。")
    }

    func persist(_ token: String) throws {
        let token = try validated(token)
        let fileManager = FileManager.default
        try fileManager.createDirectory(at: paths.appSupport, withIntermediateDirectories: true)
        try fileManager.setAttributes(
            [.posixPermissions: NSNumber(value: Int16(0o700))],
            ofItemAtPath: paths.appSupport.path
        )

        if fileManager.fileExists(atPath: paths.tunnelTokenStore.path) {
            let values = try paths.tunnelTokenStore.resourceValues(forKeys: [.isRegularFileKey, .isSymbolicLinkKey])
            guard values.isRegularFile == true, values.isSymbolicLink != true else {
                throw ValidationError("已保存的 Tunnel Token 路径不是安全的普通文件。")
            }
        }

        guard let data = token.data(using: .utf8) else {
            throw ValidationError("无法编码 Cloudflare Tunnel Token。")
        }
        try data.write(to: paths.tunnelTokenStore, options: .atomic)
        try fileManager.setAttributes(
            [.posixPermissions: NSNumber(value: Int16(0o600))],
            ofItemAtPath: paths.tunnelTokenStore.path
        )
    }

    func storedToken() throws -> String? {
        let fileManager = FileManager.default
        guard fileManager.fileExists(atPath: paths.tunnelTokenStore.path) else { return nil }
        let values = try paths.tunnelTokenStore.resourceValues(forKeys: [.isRegularFileKey, .isSymbolicLinkKey])
        guard values.isRegularFile == true, values.isSymbolicLink != true else {
            throw ValidationError("已保存的 Tunnel Token 路径不是安全的普通文件。")
        }
        let attributes = try fileManager.attributesOfItem(atPath: paths.tunnelTokenStore.path)
        let permissions = (attributes[.posixPermissions] as? NSNumber)?.intValue ?? 0o777
        guard permissions & 0o077 == 0 else {
            throw ValidationError("已保存的 Tunnel Token 权限不安全，应仅允许当前用户读取。")
        }
        let data = try Data(contentsOf: paths.tunnelTokenStore)
        guard data.count <= Self.maximumTokenBytes,
              let value = String(data: data, encoding: .utf8) else {
            throw ValidationError("已保存的 Tunnel Token 格式无效。")
        }
        return try validated(value)
    }

    private func activeTunnelToken() throws -> String? {
        guard FileManager.default.fileExists(atPath: paths.tunnelEnvironment.path) else { return nil }
        let environment = try ManagedEnvironment.load(from: paths.tunnelEnvironment)
        guard let token = environment.values["TUNNEL_TOKEN"],
              !token.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            return nil
        }
        return try validated(token)
    }

    private func validated(_ rawToken: String) throws -> String {
        let token = rawToken.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !token.isEmpty else {
            throw ValidationError("Cloudflare Tunnel Token 不能为空。")
        }
        guard !token.contains("\n"), !token.contains("\r") else {
            throw ValidationError("Cloudflare Tunnel Token 必须是单行文本。")
        }
        guard token.utf8.count <= Self.maximumTokenBytes else {
            throw ValidationError("Cloudflare Tunnel Token 长度异常。")
        }
        return token
    }
}
