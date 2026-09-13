import Foundation

enum FileAccessState: Equatable {
    case notChecked
    case accessible
    case denied
    case missing
    case unavailable

    var title: String {
        switch self {
        case .notChecked: return "未检查"
        case .accessible: return "可访问"
        case .denied: return "无权限"
        case .missing: return "目录不存在"
        case .unavailable: return "无法检测"
        }
    }
}

struct FileAccessCheck: Equatable {
    let title: String
    let url: URL
    let state: FileAccessState
}

enum FileAccessPermissionChecker {
    static func uncheckedStandardLocations(
        home: URL = FileManager.default.homeDirectoryForCurrentUser
    ) -> [FileAccessCheck] {
        standardTargets(home: home).map {
            FileAccessCheck(title: $0.title, url: $0.url, state: .notChecked)
        }
    }

    static func standardLocations(home: URL = FileManager.default.homeDirectoryForCurrentUser) -> [FileAccessCheck] {
        standardTargets(home: home).map { check(title: $0.title, url: $0.url) }
    }

    static func check(title: String, url: URL) -> FileAccessCheck {
        var isDirectory: ObjCBool = false
        guard FileManager.default.fileExists(atPath: url.path, isDirectory: &isDirectory), isDirectory.boolValue else {
            return FileAccessCheck(title: title, url: url, state: .missing)
        }
        do {
            _ = try FileManager.default.contentsOfDirectory(at: url, includingPropertiesForKeys: nil, options: [])
            return FileAccessCheck(title: title, url: url, state: .accessible)
        } catch let error as NSError {
            if error.domain == NSPOSIXErrorDomain,
               error.code == Int(EACCES) || error.code == Int(EPERM) {
                return FileAccessCheck(title: title, url: url, state: .denied)
            }
            return FileAccessCheck(title: title, url: url, state: .unavailable)
        }
    }

    private static func standardTargets(home: URL) -> [(title: String, url: URL)] {
        [
            ("桌面", home.appendingPathComponent("Desktop", isDirectory: true)),
            ("文稿", home.appendingPathComponent("Documents", isDirectory: true)),
            ("下载", home.appendingPathComponent("Downloads", isDirectory: true)),
        ]
    }
}
