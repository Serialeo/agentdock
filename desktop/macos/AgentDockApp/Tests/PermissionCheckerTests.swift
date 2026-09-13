import AppKit
import Foundation

@main
struct PermissionCheckerTests {
    static func main() throws {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("AgentDockPermissionTests-\(UUID().uuidString)", isDirectory: true)
        defer { try? FileManager.default.removeItem(at: root) }

        for directory in ["Desktop", "Documents", "Downloads"] {
            try FileManager.default.createDirectory(
                at: root.appendingPathComponent(directory, isDirectory: true),
                withIntermediateDirectories: true
            )
        }

        let unchecked = FileAccessPermissionChecker.uncheckedStandardLocations(home: root)
        precondition(unchecked.map(\.title) == ["桌面", "文稿", "下载"])
        precondition(unchecked.allSatisfy { $0.state == .notChecked })

        let standard = FileAccessPermissionChecker.standardLocations(home: root)
        precondition(standard.map(\.title) == ["桌面", "文稿", "下载"])
        precondition(standard.allSatisfy { $0.state == .accessible })

        let missing = FileAccessPermissionChecker.check(
            title: "不存在",
            url: root.appendingPathComponent("Missing", isDirectory: true)
        )
        precondition(missing.state == .missing)

        let snapshot = DesktopPermissionChecker.snapshot()
        precondition(DesktopPermissionKind.allCases.allSatisfy { snapshot.states[$0] != nil })

        let topAligned = TopAlignedStackView()
        precondition(topAligned.isFlipped)

        print("permission checker tests passed")
    }
}
