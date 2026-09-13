import Foundation

enum AppVersion {
    static var current: String {
        let raw = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String
        return display(raw)
    }

    static func matchesCoreVersion(_ output: String, expectedDisplayVersion: String = current) -> Bool {
        output.split(whereSeparator: \.isNewline).first.map(String.init)
            == "AgentDock \(expectedDisplayVersion)"
    }

    static func matchesHealthVersion(_ raw: String?, expectedDisplayVersion: String = current) -> Bool {
        display(raw) == expectedDisplayVersion
    }

    static func display(_ raw: String?) -> String {
        guard let raw else { return "未知版本" }
        let value = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !value.isEmpty else { return "未知版本" }
        return value.hasPrefix("v") ? value : "v\(value)"
    }
}
