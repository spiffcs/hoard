// The camera preview, wrapped for SwiftUI.
//
// Lifted out of the old capture screen when that came out: the scanning
// screen is the only thing that shows a preview now, and a view it depends
// on should not live in a file about something else.

import AVFoundation
import SwiftUI

/// PreviewLayerView is the AVCaptureVideoPreviewLayer, wrapped.
///
/// `.resizeAspect`, deliberately, and the caller gives the view the capture's
/// aspect ratio to match. The default `.resizeAspectFill` makes a better-looking
/// screen and a dishonest one: it crops the frame to whatever shape the view
/// happens to be, so a portrait sensor shown in a wide box appears as a
/// horizontal slice of its own middle. Framing a card against that produces a
/// capture with the card floating in the centre of a much taller frame.
struct PreviewLayerView: UIViewRepresentable {
    let session: AVCaptureSession
    /// Handed back once the layer exists, so the session can level it.
    var onConnection: (AVCaptureConnection?) -> Void = { _ in }
    /// Converts a Vision box into this layer's coordinates, handed back once
    /// the layer exists.
    ///
    /// The conversion has to live with the layer: only it knows videoGravity
    /// and the connection's rotation, and both matter. `.resizeAspect`
    /// letterboxes the picture inside the view, and the preview connection is
    /// rotated while the video tap that produced the box is deliberately not,
    /// so the two spaces differ by a quarter turn nothing else can account for.
    ///
    /// Corner-by-corner via `layerPointConverted(fromCaptureDevicePoint:)`
    /// rather than the rect converter. Both should work, but this app already
    /// proves the *inverse* of this exact call at this exact rotation:
    /// tap-to-focus feeds `captureDevicePointConverted(fromLayerPoint:)`
    /// straight to `focusPointOfInterest`, and it lands where you tap. Taking
    /// min/max of four converted points is also indifferent to how rotation
    /// reorders the corners, and extends unchanged to a tilted quad if the
    /// detector's corner points are ever plumbed through.
    var onConverter: ((@escaping (CGRect) -> CGRect) -> Void)?
    /// A tap, already converted into device space.
    ///
    /// Converted here rather than in the view because only the layer knows how
    /// the video is fitted inside it — the mapping depends on videoGravity and
    /// on the letterboxing, and reimplementing it from the aspect ratio would
    /// be a second copy of something AVFoundation already gets right.
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

    /// layerRect maps a Vision box onto this layer.
    ///
    /// Vision reports normalized boxes with a bottom-left origin against the
    /// unrotated buffer; the converter wants a top-left origin against the same
    /// unrotated picture, so the only arithmetic here is the y-flip. Everything
    /// else — rotation, gravity, letterboxing — belongs to the layer and is
    /// asked for rather than reimplemented.
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
