// macOS only. ScanKit is the Mac end of the link: the translator, and the
// optional mirror window it can draw. See Package.swift.
#if os(macOS)

import AppKit

/// PreviewView hosts the mirror window's preview layer and keeps the HUD
/// pinned to it.
///
/// It handles no keys. The phone is the camera and the terminal is the
/// controller; a mirror window that also accepted a shutter press would be a
/// third place to drive the session from.
final class PreviewView: NSView {
    /// The price HUD, kept sized to the view.
    var hud: PriceHUD?

    override func layout() {
        super.layout()
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        hud?.layout(bounds: bounds)
        CATransaction.commit()
    }

    /// A window dragged to a different-density display re-rasterizes the HUD
    /// text, or it renders blurry there.
    override func viewDidChangeBackingProperties() {
        super.viewDidChangeBackingProperties()
        if let scale = window?.backingScaleFactor { hud?.setScale(scale) }
    }
}

#endif
