import Foundation

enum ServicePortValidation {
    static let allowedRange = 1024...65535

    static func validate(_ port: Int) throws {
        guard allowedRange.contains(port) else {
            throw ValidationError("普通用户服务端口必须在 1024 到 65535 之间。")
        }
    }
}
