import Darwin
import Foundation

struct DesktopUpdateServiceState: Codable {
    static let schemaVersion = 1

    let schemaVersion: Int
    let coreEnabled: Bool
    let tunnelEnabled: Bool

    private enum CodingKeys: String, CodingKey {
        case schemaVersion = "schema_version"
        case coreEnabled = "core_enabled"
        case tunnelEnabled = "tunnel_enabled"
    }

    init(coreEnabled: Bool, tunnelEnabled: Bool) {
        schemaVersion = Self.schemaVersion
        self.coreEnabled = coreEnabled
        self.tunnelEnabled = tunnelEnabled
    }

    static func load(from path: URL) throws -> DesktopUpdateServiceState? {
        guard FileManager.default.fileExists(atPath: path.path) else { return nil }
        let data = try Data(contentsOf: path)
        let state = try JSONDecoder().decode(DesktopUpdateServiceState.self, from: data)
        guard state.schemaVersion == Self.schemaVersion else {
            throw ValidationError("AgentDock 更新服务状态版本不受支持。")
        }
        return state
    }

    func write(to path: URL) throws {
        let fileManager = FileManager.default
        let directory = path.deletingLastPathComponent()
        try fileManager.createDirectory(at: directory, withIntermediateDirectories: true)
        try fileManager.setAttributes([.posixPermissions: 0o700], ofItemAtPath: directory.path)

        var data = try JSONEncoder().encode(self)
        data.append(0x0a)
        let temporary = directory.appendingPathComponent(".\(path.lastPathComponent).tmp.\(UUID().uuidString)")
        defer { try? fileManager.removeItem(at: temporary) }
        guard fileManager.createFile(atPath: temporary.path, contents: data, attributes: [.posixPermissions: 0o600]) else {
            throw ValidationError("无法创建 AgentDock 更新服务状态。")
        }
        try fileManager.setAttributes([.posixPermissions: 0o600], ofItemAtPath: temporary.path)
        if Darwin.rename(temporary.path, path.path) != 0 {
            throw ValidationError("无法保存 AgentDock 更新服务状态：\(String(cString: strerror(errno)))")
        }
    }

    static func remove(at path: URL) {
        try? FileManager.default.removeItem(at: path)
    }
}
