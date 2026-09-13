import Foundation

struct PublicEndpointCheckResult: Equatable, Sendable {
    let isReachable: Bool
    let message: String
    let latencyMilliseconds: Int?
}

final class PublicEndpointChecker: @unchecked Sendable {
    private let session: URLSession

    init(session: URLSession = .shared) {
        self.session = session
    }

    func check(publicMCPURL: URL) async -> PublicEndpointCheckResult {
        guard let healthURL = Self.healthURL(from: publicMCPURL) else {
            return PublicEndpointCheckResult(
                isReachable: false,
                message: "公网地址格式无效",
                latencyMilliseconds: nil
            )
        }

        var request = URLRequest(
            url: healthURL,
            cachePolicy: .reloadIgnoringLocalAndRemoteCacheData,
            timeoutInterval: 8
        )
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        request.setValue("no-cache", forHTTPHeaderField: "Cache-Control")

        let startedAt = Date()
        do {
            let (data, response) = try await session.data(for: request)
            let latency = max(0, Int(Date().timeIntervalSince(startedAt) * 1_000))
            guard let response = response as? HTTPURLResponse else {
                return PublicEndpointCheckResult(
                    isReachable: false,
                    message: "公网地址没有返回 HTTP 响应",
                    latencyMilliseconds: latency
                )
            }
            guard response.statusCode == 200 else {
                return PublicEndpointCheckResult(
                    isReachable: false,
                    message: "公网地址返回 HTTP \(response.statusCode)",
                    latencyMilliseconds: latency
                )
            }
            guard let payload = try? JSONDecoder().decode(PublicHealthPayload.self, from: data), payload.ok else {
                return PublicEndpointCheckResult(
                    isReachable: false,
                    message: "公网健康检查返回无效数据",
                    latencyMilliseconds: latency
                )
            }
            return PublicEndpointCheckResult(
                isReachable: true,
                message: "可正常访问",
                latencyMilliseconds: latency
            )
        } catch {
            return PublicEndpointCheckResult(
                isReachable: false,
                message: Self.networkErrorMessage(error),
                latencyMilliseconds: nil
            )
        }
    }

    static func healthURL(from publicMCPURL: URL) -> URL? {
        guard var components = URLComponents(url: publicMCPURL, resolvingAgainstBaseURL: false),
              components.scheme?.lowercased() == "https",
              components.host?.isEmpty == false,
              components.user == nil,
              components.password == nil else {
            return nil
        }
        components.path = "/healthz"
        components.query = nil
        components.fragment = nil
        return components.url
    }

    private static func networkErrorMessage(_ error: Error) -> String {
        guard let urlError = error as? URLError else {
            return error.localizedDescription
        }
        switch urlError.code {
        case .timedOut:
            return "公网访问超时"
        case .cannotFindHost, .dnsLookupFailed:
            return "无法解析公网域名"
        case .cannotConnectToHost:
            return "无法连接公网地址"
        case .networkConnectionLost:
            return "公网连接中断"
        case .notConnectedToInternet:
            return "当前电脑未连接网络"
        case .cancelled:
            return "检测已取消"
        default:
            return urlError.localizedDescription
        }
    }
}

private struct PublicHealthPayload: Decodable {
    let ok: Bool
}
