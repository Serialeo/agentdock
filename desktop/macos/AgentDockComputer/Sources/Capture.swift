import CoreGraphics
import AppKit
import ScreenCaptureKit
import CoreImage
import CoreMedia
import Darwin

struct CapturedFrame {
    let image: CGImage
    let displayTime: UInt64
    let receivedAt: Date
    let contentRect: CGRect
    let scaleFactor: CGFloat
    let contentScale: CGFloat
}

final class FrameCollector: NSObject, SCStreamOutput, SCStreamDelegate {
    private let lock = NSLock()
    private var latest: CapturedFrame?
    private var failure: Error?
    private let imageContext = CIContext()

    func stream(_ stream: SCStream, didOutputSampleBuffer sampleBuffer: CMSampleBuffer, of type: SCStreamOutputType) {
        guard type == .screen, sampleBuffer.isValid,
              let entries = CMSampleBufferGetSampleAttachmentsArray(sampleBuffer, createIfNecessary: false) as? [[SCStreamFrameInfo: Any]],
              let entry = entries.first,
              let status = entry[.status] as? Int, status == SCFrameStatus.complete.rawValue else { return }
        guard let displayTime = (entry[.displayTime] as? NSNumber)?.uint64Value,
              let dictionary = entry[.contentRect] as? [String: Any],
              let contentRect = CGRect(dictionaryRepresentation: dictionary as CFDictionary),
              let scale = entry[.scaleFactor] as? NSNumber,
              let contentScale = entry[.contentScale] as? NSNumber,
              let pixels = CMSampleBufferGetImageBuffer(sampleBuffer) else {
            setFailure(ProbeFailure("complete frame is missing ScreenCaptureKit metadata")); return
        }
        guard scale.doubleValue.isFinite, scale.doubleValue > 0,
              contentScale.doubleValue.isFinite, contentScale.doubleValue > 0 else {
            setFailure(ProbeFailure("invalid ScreenCaptureKit scale metadata")); return
        }
        let ci = CIImage(cvPixelBuffer: pixels)
        guard let image = imageContext.createCGImage(ci, from: ci.extent, format: .RGBA8,
                                                     colorSpace: CGColorSpace(name: CGColorSpace.sRGB)) else {
            setFailure(ProbeFailure("could not materialize capture frame")); return
        }
        lock.lock()
        latest = CapturedFrame(image: image, displayTime: displayTime, receivedAt: Date(), contentRect: contentRect,
                               scaleFactor: CGFloat(scale.doubleValue), contentScale: CGFloat(contentScale.doubleValue))
        lock.unlock()
    }
    func stream(_ stream: SCStream, didStopWithError error: Error) { setFailure(error) }
    private func setFailure(_ error: Error) { lock.lock(); failure = error; lock.unlock() }
    private func snapshot() throws -> CapturedFrame? {
        lock.lock(); defer { lock.unlock() }
        if let error = failure { throw error }
        return latest
    }
    func next(after ticks: UInt64, deadline: Date = Date().addingTimeInterval(5)) async throws -> CapturedFrame {
        while Date() < deadline {
            try Task.checkCancellation()
            if let frame = try snapshot(), frame.displayTime > ticks { return frame }
            try await Task.sleep(nanoseconds: 20_000_000)
        }
        throw ProbeFailure("no complete fresh ScreenCaptureKit frame before deadline")
    }
}

func savePNG(_ frame: CapturedFrame, to url: URL) throws {
    let representation = NSBitmapImageRep(cgImage: frame.image)
    guard let data = representation.representation(using: .png, properties: [:]) else { throw ProbeFailure("PNG encoding failed") }
    try data.write(to: url, options: .atomic)
    try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: url.path)
}
func sampleRGB(_ image: CGImage, at point: CGPoint) throws -> [Int] {
    let x = Int(point.x.rounded()), y = Int(point.y.rounded())
    guard x >= 0, y >= 0, x < image.width, y < image.height,
          let pixel = image.cropping(to: CGRect(x: x, y: y, width: 1, height: 1)) else { throw ProbeFailure("sample outside captured image") }
    var bytes = [UInt8](repeating: 0, count: 4)
    let ok = bytes.withUnsafeMutableBytes { buffer -> Bool in
        guard let context = CGContext(data: buffer.baseAddress, width: 1, height: 1, bitsPerComponent: 8, bytesPerRow: 4,
                                      space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else { return false }
        context.draw(pixel, in: CGRect(x: 0, y: 0, width: 1, height: 1))
        return true
    }
    guard ok else { throw ProbeFailure("pixel sampling failed") }
    return bytes.prefix(3).map(Int.init)
}
