import AVFoundation
import SwiftUI

struct PreviewLayerView: UIViewRepresentable {
    let session: AVCaptureSession
    var onConnection: (AVCaptureConnection?) -> Void = { _ in }
    var onConverter: ((@escaping (CGRect) -> CGRect) -> Void)?
    var onTap: (CGPoint) -> Void = { _ in }

    func makeUIView(context: Context) -> PreviewUIView {
        let v = PreviewUIView()
        v.previewLayer.session = session
        v.previewLayer.videoGravity = .resizeAspect
        DispatchQueue.main.async { onConnection(v.previewLayer.connection) }
        v.onTap = onTap
        if let onConverter {
            DispatchQueue.main.async { [weak v] in
                onConverter { box in
                    guard let v else { return .zero }
                    return v.layerRect(forVisionBox: box)
                }
            }
        }
        return v
    }

    func updateUIView(_ uiView: PreviewUIView, context: Context) {}
}

final class PreviewUIView: UIView {
    override class var layerClass: AnyClass { AVCaptureVideoPreviewLayer.self }
    var previewLayer: AVCaptureVideoPreviewLayer { layer as! AVCaptureVideoPreviewLayer }

    var onTap: (CGPoint) -> Void = { _ in } {
        didSet { installTapRecognizer() }
    }

    private func installTapRecognizer() {
        guard gestureRecognizers?.isEmpty ?? true else { return }
        addGestureRecognizer(
            UITapGestureRecognizer(target: self, action: #selector(handleTap)))
    }

    func layerRect(forVisionBox box: CGRect) -> CGRect {
        let d = CGRect(x: box.minX, y: 1 - box.maxY,
                       width: box.width, height: box.height)
        let points = [
            CGPoint(x: d.minX, y: d.minY), CGPoint(x: d.maxX, y: d.minY),
            CGPoint(x: d.maxX, y: d.maxY), CGPoint(x: d.minX, y: d.maxY),
        ].map { previewLayer.layerPointConverted(fromCaptureDevicePoint: $0) }
        let xs = points.map(\.x), ys = points.map(\.y)
        return CGRect(x: xs.min()!, y: ys.min()!,
                      width: xs.max()! - xs.min()!, height: ys.max()! - ys.min()!)
    }

    @objc private func handleTap(_ g: UITapGestureRecognizer) {
        let point = previewLayer.captureDevicePointConverted(
            fromLayerPoint: g.location(in: self))
        onTap(point)
    }
}
