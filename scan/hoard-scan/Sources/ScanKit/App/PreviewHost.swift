// macOS only. This is the camera, window and HUD half of ScanKit; the read
// pipeline under Core/ is what compiles for iOS. See Package.swift.
#if os(macOS)

import AVFoundation
import AppKit

/// What the outline cue and the price HUD need from whatever is showing video.
///
/// Both used to take an `AVCaptureVideoPreviewLayer` directly, which was true
/// until the video started arriving from a phone over the network instead of
/// from a local capture session. What they actually need turns out to be three
/// things, two of which every `CALayer` already has — so the protocol is small
/// and the local case conforms for free.
///
/// The third is `videoRect`, and it is the one that matters. `.resizeAspect`
/// letterboxes the frame inside the view, and a HUD pinned to the *view* corner
/// lands in the black bars beside a portrait feed. Both overlays ask where the
/// picture actually is, not where the view is.
protocol PreviewHost: AnyObject {
    var bounds: CGRect { get }
    func addSublayer(_ layer: CALayer)
    /// Where the video actually shows inside `bounds`, letterboxing excluded.
    var videoRect: CGRect { get }
}

extension AVCaptureVideoPreviewLayer: PreviewHost {
    var videoRect: CGRect {
        layerRectConverted(fromMetadataOutputRect: CGRect(x: 0, y: 0, width: 1, height: 1))
    }
}

/// RemotePreviewLayer shows JPEG frames pushed from the phone.
///
/// A plain layer with its `contents` swapped, rather than anything clever: the
/// frames arrive already the right way up (the phone applies the rotation, the
/// same one it bakes into the still) and at whatever size the phone chose to
/// send. All this has to do is letterbox them the way `.resizeAspect` would, so
/// that everything drawn on top lands in the same place it does locally.
final class RemotePreviewLayer: CALayer, PreviewHost {
    /// The pixel size of the last frame shown, for the aspect maths.
    private var frameSize: CGSize = .zero

    override init() {
        super.init()
        contentsGravity = .resizeAspect
        backgroundColor = NSColor.black.cgColor
    }

    override init(layer: Any) {
        super.init(layer: layer)
    }

    required init?(coder: NSCoder) { nil }

    /// show puts a decoded frame on screen.
    ///
    /// Implicit animation off: a preview updating ten times a second would
    /// otherwise cross-fade every frame into the next, which reads as a smear
    /// rather than a video.
    func show(_ image: CGImage) {
        frameSize = CGSize(width: image.width, height: image.height)
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        contents = image
        CATransaction.commit()
    }

    /// videoRect is the aspect-fitted frame inside the layer — the same
    /// rectangle `AVCaptureVideoPreviewLayer` reports for a `.resizeAspect`
    /// preview, computed rather than asked for.
    ///
    /// Falls back to the whole layer before any frame has arrived, where the
    /// two are the same thing anyway.
    var videoRect: CGRect {
        guard frameSize.width > 0, frameSize.height > 0,
              bounds.width > 0, bounds.height > 0
        else { return bounds }
        let scale = min(bounds.width / frameSize.width, bounds.height / frameSize.height)
        let size = CGSize(width: frameSize.width * scale, height: frameSize.height * scale)
        return CGRect(
            x: bounds.midX - size.width / 2,
            y: bounds.midY - size.height / 2,
            width: size.width,
            height: size.height)
    }
}

#endif
