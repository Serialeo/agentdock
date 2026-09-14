import CoreGraphics
import Foundation

struct CaptureGeometry {
    let width: Int
    let height: Int
    let desktop: CGRect

    func validate() throws {
        guard width > 0, height > 0, desktop.width > 0, desktop.height > 0,
              [desktop.minX, desktop.minY, desktop.width, desktop.height].allSatisfy({ $0.isFinite }) else {
            throw ProbeFailure("invalid capture geometry")
        }
    }
    func native(_ point: CGPoint) throws -> CGPoint {
        try validate()
        guard point.x.isFinite, point.y.isFinite, point.x >= 0, point.y >= 0,
              point.x < CGFloat(width), point.y < CGFloat(height) else { throw ProbeFailure("image point out of bounds") }
        return CGPoint(x: desktop.minX + (point.x + 0.5) * desktop.width / CGFloat(width),
                       y: desktop.minY + (point.y + 0.5) * desktop.height / CGFloat(height))
    }
    func image(_ point: CGPoint) throws -> CGPoint {
        try validate()
        guard point.x.isFinite, point.y.isFinite, point.x >= desktop.minX, point.y >= desktop.minY,
              point.x < desktop.maxX, point.y < desktop.maxY else { throw ProbeFailure("native point out of bounds") }
        return CGPoint(x: (point.x - desktop.minX) * CGFloat(width) / desktop.width - 0.5,
                       y: (point.y - desktop.minY) * CGFloat(height) / desktop.height - 0.5)
    }
}
struct ProbeFailure: Error, CustomStringConvertible {
    let description: String
    init(_ message: String) { description = message }
}

// Frame metadata uses a content rectangle plus a point-to-pixel scale.
// P0 accepts only a rectangle that covers the entire output raster.
func validateFullDisplayContent(_ rect: CGRect, scaleFactor: CGFloat, width: Int, height: Int) throws {
    guard width > 0, height > 0, scaleFactor.isFinite, scaleFactor > 0,
          [rect.minX, rect.minY, rect.width, rect.height].allSatisfy({ $0.isFinite }),
          abs(rect.minX * scaleFactor) < 0.5, abs(rect.minY * scaleFactor) < 0.5,
          abs(rect.width * scaleFactor - CGFloat(width)) < 1,
          abs(rect.height * scaleFactor - CGFloat(height)) < 1 else {
        throw ProbeFailure("ScreenCaptureKit content metadata does not cover the full raster; mapping requires calibration")
    }
}
