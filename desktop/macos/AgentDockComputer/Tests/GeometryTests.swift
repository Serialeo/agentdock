import CoreGraphics
import Foundation
@main
struct GeometryTests {
    static func main() throws {
        let geometry = CaptureGeometry(width: 1280, height: 720, desktop: CGRect(x: -2560, y: -100, width: 2560, height: 1440))
        let native = try geometry.native(CGPoint(x: 100, y: 50))
        precondition(native == CGPoint(x: -2359, y: 1))
        let image = try geometry.image(native)
        precondition(image == CGPoint(x: 100, y: 50))
        for point in [CGPoint(x: -1, y: 0), CGPoint(x: 1280, y: 0), CGPoint(x: 0, y: 720), CGPoint(x: CGFloat.nan, y: 0)] {
            do { _ = try geometry.native(point); fatalError("invalid point accepted") } catch is ProbeFailure {}
        }
        try validateFullDisplayContent(CGRect(x: 0, y: 0, width: 1440, height: 900), scaleFactor: 2, width: 2880, height: 1800)
        try validateFullDisplayContent(CGRect(x: 0, y: 0, width: 1920, height: 1080), scaleFactor: 1, width: 1920, height: 1080)
        for rect in [CGRect(x: 10, y: 0, width: 1440, height: 900), CGRect(x: 0, y: 0, width: 1400, height: 900)] {
            do { try validateFullDisplayContent(rect, scaleFactor: 2, width: 2880, height: 1800); fatalError("padding/crop accepted") } catch is ProbeFailure {}
        }
        print("CaptureGeometry tests passed")
    }
}
