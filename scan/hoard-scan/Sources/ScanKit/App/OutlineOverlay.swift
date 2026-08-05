// macOS only. This is the camera, window and HUD half of ScanKit; the read
// pipeline under Core/ is what compiles for iOS. See Package.swift.
#if os(macOS)

import AVFoundation
import AppKit

/// OutlineOverlay draws the one cue the user sees: four viewfinder brackets at
/// the corners of the single dominant rectangle — yellow while it settles,
/// green once it's shot.
///
/// It is presentation, not machinery. The trigger has already decided by the
/// time any of this runs, and nothing here can change what gets captured; the
/// job is to make the app look calmly locked onto a card, and to make a
/// reluctant trigger diagnosable at a glance — no brackets means no rectangle,
/// jumping brackets mean the scene never reads as still.
///
/// Drawing the raw per-sample truth does the opposite of calm. Vision drops the
/// card on roughly two samples in five, so hiding on an empty sample blinked
/// the brackets five times a second; the largest box changes between samples,
/// so the cue teleported between candidates; and disabling implicit animation
/// made every one of those a hard jump. The result read as chaos and looked
/// broken even while the scan was succeeding. So: hold the last cue briefly
/// through a blink, ease between positions instead of snapping, and keep
/// tracking the same box rather than whichever is largest this instant.
///
/// Brackets rather than full rectangles, and one rather than several, for the
/// same reason: re-fit on every Vision sample, full outlines slid and flickered
/// like the display was struggling, and the extra boxes were desk clutter and
/// deck boxes the trigger weighs but the user shouldn't have to look at.
///
/// Main-thread only, like the rest of the controller's state.
final class OutlineOverlay {
    private let shape = CAShapeLayer()
    /// The layer the cue is drawn over, which also owns the mapping from
    /// buffer space into what is actually on screen. Weak: the preview layer
    /// belongs to the window, and the cue must not keep it alive past close.
    private weak var preview: (any PreviewHost)?

    /// The rectangles the trigger last saw, kept so a phase change can recolor
    /// the cue without waiting for the next sample.
    private var lastBoxes: [CGRect] = []
    /// The box the cue is currently drawn around, and when it was last
    /// confirmed by a real detection — together they let the bracket ride out
    /// a detector blink instead of flickering off.
    private var box: CGRect?
    private var heldSince: Date?

    /// attach hangs the cue's layer off the preview layer. Call it before the
    /// HUD attaches, so the price flash sits above the brackets.
    func attach(to preview: any PreviewHost, bounds: CGRect) {
        self.preview = preview
        shape.frame = bounds
        shape.fillColor = nil
        shape.lineWidth = 3
        // Rounded caps soften the corner brackets — see addCornerBrackets.
        shape.lineCap = .round
        shape.lineJoin = .round
        shape.strokeColor = NSColor.systemYellow.cgColor
        shape.isHidden = true
        preview.addSublayer(shape)
    }

    func layout(bounds: CGRect) {
        shape.frame = bounds
    }

    /// show draws the cue for the rectangles the trigger is currently
    /// considering. `rotation` is the same clockwise turn the preview shows.
    func show(_ boxes: [CGRect], phase: AutoTrigger.Phase, rotation: Int) {
        lastBoxes = boxes
        draw(boxes, phase: phase, rotation: rotation)
    }

    /// redraw repaints the boxes already on screen, for a phase change that
    /// only alters the colour and weight.
    func redraw(phase: AutoTrigger.Phase, rotation: Int) {
        draw(lastBoxes, phase: phase, rotation: rotation)
    }

    private func draw(_ boxes: [CGRect], phase: AutoTrigger.Phase, rotation: Int) {
        guard preview != nil else { return }
        if phase == .off {
            heldSince = nil
            box = nil
            hide()
            return
        }
        // Prefer the box nearest the one already being drawn, so a steady hand
        // keeps a steady bracket even when the detector reorders its results.
        var main: CGRect?
        if let held = box, !boxes.isEmpty {
            main = boxes.min {
                hypot($0.midX - held.midX, $0.midY - held.midY)
                    < hypot($1.midX - held.midX, $1.midY - held.midY)
            }
        } else {
            main = boxes.max { $0.width * $0.height < $1.width * $1.height }
        }
        if let m = main {
            box = m
            heldSince = Date()
        } else if let since = heldSince,
                  Date().timeIntervalSince(since) < OutlineTuning.holdSeconds {
            main = box      // a blink, not a departure: keep the cue up
        } else {
            heldSince = nil
            box = nil
        }
        guard let visible = main, let r = bracketRect(for: visible, rotation: rotation) else {
            hide()
            return
        }
        let path = CGMutablePath()
        addCornerBrackets(to: path, around: r)
        CATransaction.begin()
        // A short ease is what turns jitter into a cue that feels alive rather
        // than nervous. The shutter itself stays instant — nothing should lag
        // behind the moment of capture.
        CATransaction.setAnimationDuration(phase == .capturing ? 0 : OutlineTuning.easeSeconds)
        shape.path = path
        shape.strokeColor = color(for: phase)
        shape.lineWidth = phase == .capturing ? 5 : 3
        shape.isHidden = false
        CATransaction.commit()
    }

    private func hide() {
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        shape.isHidden = true
        CATransaction.commit()
    }

    /// bracketRect maps a Vision box from the unrotated analysis buffer into
    /// preview-layer coordinates by hand. The obvious route —
    /// layerRectConverted(fromMetadataOutputRect:) — turns the box by the
    /// wrong sense when the preview is rotated: at 270° the cue landed
    /// mirrored through the frame's center, its bottom edge framing the
    /// card's middle (observed live). The capture path already owns the
    /// correct convention — the preview connection and rotatedImage both
    /// turn the buffer *clockwise* by effectiveRotation, and OCR reading
    /// those captures upright proves it — so the box takes the same
    /// clockwise turn, and only the full-frame extent (rotation-symmetric,
    /// so immune to the converter's sense) is asked of the layer.
    private func bracketRect(for b: CGRect, rotation: Int) -> CGRect? {
        guard let preview else { return nil }
        let video = preview.videoRect
        guard !video.isNull, video.width > 1, video.height > 1 else { return nil }
        // Buffer space, normalized, top-left origin.
        var r = CGRect(x: b.minX, y: 1 - b.maxY, width: b.width, height: b.height)
        // The same clockwise turn the preview shows.
        switch ((rotation % 360) + 360) % 360 {
        case 90:
            r = CGRect(x: 1 - r.maxY, y: r.minX, width: r.height, height: r.width)
        case 180:
            r = CGRect(x: 1 - r.maxX, y: 1 - r.maxY, width: r.width, height: r.height)
        case 270:
            r = CGRect(x: r.minY, y: 1 - r.maxX, width: r.height, height: r.width)
        default:
            break
        }
        // Scale into the displayed video area; layer coordinates are y-up,
        // so the display-space top edge is the layer-space maxY.
        return CGRect(x: video.minX + r.minX * video.width,
                      y: video.minY + (1 - r.maxY) * video.height,
                      width: r.width * video.width,
                      height: r.height * video.height)
    }

    /// addCornerBrackets appends four L-shaped marks squaring a rect's
    /// corners, arms proportional to the card but capped so a close-up card
    /// doesn't wear giant angles.
    private func addCornerBrackets(to path: CGMutablePath, around r: CGRect) {
        let arm = min(min(r.width, r.height) * 0.22, 34)
        // Bottom-left, bottom-right, top-right, top-left (layer coords, y-up).
        path.move(to: CGPoint(x: r.minX, y: r.minY + arm))
        path.addLine(to: CGPoint(x: r.minX, y: r.minY))
        path.addLine(to: CGPoint(x: r.minX + arm, y: r.minY))
        path.move(to: CGPoint(x: r.maxX - arm, y: r.minY))
        path.addLine(to: CGPoint(x: r.maxX, y: r.minY))
        path.addLine(to: CGPoint(x: r.maxX, y: r.minY + arm))
        path.move(to: CGPoint(x: r.maxX, y: r.maxY - arm))
        path.addLine(to: CGPoint(x: r.maxX, y: r.maxY))
        path.addLine(to: CGPoint(x: r.maxX - arm, y: r.maxY))
        path.move(to: CGPoint(x: r.minX + arm, y: r.maxY))
        path.addLine(to: CGPoint(x: r.minX, y: r.maxY))
        path.addLine(to: CGPoint(x: r.minX, y: r.maxY - arm))
    }

    private func color(for phase: AutoTrigger.Phase) -> CGColor {
        switch phase {
        case .stabilizing:
            return NSColor.systemYellow.cgColor
        case .capturing:
            return NSColor.systemGreen.cgColor
        case .hold:
            // Parked on the shot card: a quiet green says "already counted".
            return NSColor.systemGreen.withAlphaComponent(0.45).cgColor
        case .searching, .off:
            return NSColor.white.withAlphaComponent(0.35).cgColor
        }
    }
}

#endif
