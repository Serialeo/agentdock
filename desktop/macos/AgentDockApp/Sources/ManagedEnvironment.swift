import Foundation

struct ManagedEnvironment {
    let originalText: String
    let values: [String: String]

    static func load(from url: URL) throws -> ManagedEnvironment {
        let data = try Data(contentsOf: url)
        guard let text = String(data: data, encoding: .utf8) else {
            throw ValidationError("AgentDock 配置文件不是有效的 UTF-8 文本。")
        }
        return ManagedEnvironment(originalText: text, values: parseValues(text))
    }

    func dataByUpdating(_ replacements: [String: String], removing removals: Set<String> = []) throws -> Data {
        let editable = Set(ServiceConfiguration.editableKeys)
        let requested = Set(replacements.keys)
        guard requested.isSubset(of: editable) else {
            let rejected = requested.subtracting(editable).sorted().joined(separator: ", ")
            throw ValidationError("包含不允许由图形界面修改的配置项：\(rejected)")
        }
        let removable = editable.union(ServiceConfiguration.removableLegacyKeys)
        guard removals.isSubset(of: removable) else {
            let rejected = removals.subtracting(removable).sorted().joined(separator: ", ")
            throw ValidationError("包含不允许由图形界面删除的配置项：\(rejected)")
        }
        guard requested.isDisjoint(with: removals) else {
            throw ValidationError("同一配置项不能同时更新和删除。")
        }

        var pending = replacements
        var output: [String] = []
        let lines = originalText.split(separator: "\n", omittingEmptySubsequences: false).map(String.init)
        for line in lines {
            guard let key = Self.assignmentKey(line) else {
                output.append(line)
                continue
            }
            if removals.contains(key) {
                continue
            }
            guard replacements.keys.contains(key) else {
                output.append(line)
                continue
            }
            guard let replacement = pending.removeValue(forKey: key) else {
                // 已处理过该键，清理后续重复定义。
                continue
            }
            output.append("\(key)=\(Self.shellQuote(replacement))")
        }
        for key in pending.keys.sorted() {
            if let value = pending[key] {
                output.append("\(key)=\(Self.shellQuote(value))")
            }
        }
        while output.last == "" { output.removeLast() }
        output.append("")
        guard let data = output.joined(separator: "\n").data(using: .utf8) else {
            throw ValidationError("无法编码 AgentDock 配置文件。")
        }
        return data
    }

    static func parseValues(_ text: String) -> [String: String] {
        var values: [String: String] = [:]
        for rawLine in text.split(whereSeparator: \.isNewline) {
            var line = rawLine.trimmingCharacters(in: .whitespaces)
            if line.isEmpty || line.hasPrefix("#") { continue }
            if line.hasPrefix("export ") {
                line = String(line.dropFirst("export ".count)).trimmingCharacters(in: .whitespaces)
            }
            guard let separator = line.firstIndex(of: "=") else { continue }
            let key = String(line[..<separator]).trimmingCharacters(in: .whitespaces)
            guard key.range(of: "^[A-Z_][A-Z0-9_]*$", options: .regularExpression) != nil else { continue }
            let rawValue = String(line[line.index(after: separator)...])
            values[key] = decodeShellValue(rawValue)
        }
        return values
    }

    private static func assignmentKey(_ rawLine: String) -> String? {
        var line = rawLine.trimmingCharacters(in: .whitespaces)
        if line.isEmpty || line.hasPrefix("#") { return nil }
        if line.hasPrefix("export ") {
            line = String(line.dropFirst("export ".count)).trimmingCharacters(in: .whitespaces)
        }
        guard let separator = line.firstIndex(of: "=") else { return nil }
        let key = String(line[..<separator]).trimmingCharacters(in: .whitespaces)
        guard key.range(of: "^[A-Z_][A-Z0-9_]*$", options: .regularExpression) != nil else { return nil }
        return key
    }

    static func shellQuote(_ value: String) -> String {
        if value.isEmpty { return "''" }
        return "'" + value.replacingOccurrences(of: "'", with: "'\\''") + "'"
    }

    static func decodeShellValue(_ raw: String) -> String {
        let input = Array(raw.trimmingCharacters(in: .whitespaces))
        var result = ""
        var index = 0
        var quote: Character?
        while index < input.count {
            let character = input[index]
            if let activeQuote = quote {
                if character == activeQuote {
                    quote = nil
                } else if activeQuote == "\"", character == "\\", index + 1 < input.count {
                    index += 1
                    result.append(input[index])
                } else {
                    result.append(character)
                }
            } else if character == "'" || character == "\"" {
                quote = character
            } else if character == "\\", index + 1 < input.count {
                index += 1
                result.append(input[index])
            } else {
                result.append(character)
            }
            index += 1
        }
        return result
    }
}
