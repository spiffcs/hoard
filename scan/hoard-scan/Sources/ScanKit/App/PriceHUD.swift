// macOS only. ScanKit is the Mac end of the link: the translator, and the
// optional mirror window it can draw. See Package.swift.
#if os(macOS)

import AVFoundation
import AppKit
import ScanWire

// MARK: - Price HUD

/// PriceHUD renders resolved prices over the camera preview: a transient
/// tier-styled flash of the amount just scanned, a persistent running session
/// total in the corner, and a coin shower for jackpots. All Core Animation
/// layers on top of the preview layer; all animation is *explicit*, so the
/// disabled-actions transactions the outline drawing uses never interfere.
///
/// Layer coordinates are y-up (the preview layer is not flipped): the total
/// pins near y=0 (the bottom), coins rain toward -y, flashes float toward +y.
final class PriceHUD {
    private let container = CALayer()
    private let totalLayer = CATextLayer()
    private var scale: CGFloat = 2
    private weak var preview: (any PreviewHost)?

    private static let gold = NSColor(calibratedRed: 1, green: 0.84, blue: 0, alpha: 1)

    /// videoRect is where the video actually shows: .resizeAspect letterboxes
    /// the frame inside the view, and a HUD pinned to the *view* corner lands
    /// in the black bars beside a portrait feed. Falls back to the whole view
    /// when there is no video to measure (the demo, or before the session
    /// starts), where the two are the same thing anyway.
    private var videoRect: CGRect {
        let bounds = container.bounds
        guard let preview else { return bounds }
        let r = preview.videoRect
        guard !r.isNull, !r.isInfinite, r.width > 40, r.height > 40 else { return bounds }
        return r.intersection(bounds)
    }

    /// attach hangs the HUD's layers off the preview layer, above the outline.
    func attach(to host: any PreviewHost, scale: CGFloat) {
        self.scale = scale
        self.preview = host
        container.frame = host.bounds
        totalLayer.font = NSFont.monospacedDigitSystemFont(ofSize: 26, weight: .bold)
        totalLayer.fontSize = 26
        totalLayer.alignmentMode = .right
        totalLayer.foregroundColor = NSColor.white.cgColor
        totalLayer.shadowColor = NSColor.black.cgColor
        totalLayer.shadowOpacity = 0.9
        totalLayer.shadowRadius = 2
        totalLayer.shadowOffset = .zero
        totalLayer.contentsScale = scale
        totalLayer.isHidden = true
        container.addSublayer(totalLayer)
        host.addSublayer(container)
        layout(bounds: host.bounds)
    }

    /// layout re-pins the HUD to the view. Called from PreviewView.layout()
    /// inside its disabled-actions transaction, so resizes never animate.
    func layout(bounds: CGRect) {
        container.frame = bounds
        repinTotal()
    }

    /// repinTotal puts the running total just inside the video frame's top
    /// right (layer coords are y-up: the top is maxY). Re-run on every show
    /// too, not just view layout — the video rect settles after the session
    /// starts and moves when the preview is rotated, neither of which lays
    /// out the view.
    private func repinTotal() {
        let rect = videoRect
        totalLayer.frame = CGRect(x: rect.maxX - 252, y: rect.maxY - 48, width: 240, height: 34)
    }

    /// setScale re-rasterizes the text for the current display — without this
    /// a window dragged to a different-density screen renders blurry.
    func setScale(_ s: CGFloat) {
        scale = s
        totalLayer.contentsScale = s
    }

    /// show renders one result: tier flash (and jackpot shower), then the
    /// silent total update.
    func show(_ cmd: HUDCommand) {
        if let tier = cmd.tier {
            flash(amount: cmd.amount, tier: tier)
            if tier == "jackpot" { coinShower() }
        }
        if let total = cmd.total { setTotal(total) }
    }

    private func setTotal(_ total: Double) {
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        repinTotal()
        totalLayer.string = String(format: "$%.2f", total)
        totalLayer.isHidden = false
        CATransaction.commit()
        // A brief gold pulse, so silently-landing money (a review confirm) is
        // still visible in the corner of the eye.
        let pulse = CABasicAnimation(keyPath: "foregroundColor")
        pulse.fromValue = Self.gold.cgColor
        pulse.toValue = NSColor.white.cgColor
        pulse.duration = 0.6
        totalLayer.add(pulse, forKey: "pulse")
    }

    /// flash floats the just-scanned amount up the middle of the frame. Each
    /// flash is its own layer with a timed removal, so rapid scans overlap
    /// harmlessly instead of fighting over one layer's animations.
    private func flash(amount: Double?, tier: String) {
        let text: String
        if tier == "review" {
            // A queued card isn't a win yet: the terminal has the review, and
            // the printing (so the price) is still unverified.
            text = "Needs Review"
        } else if let amount {
            text = String(format: "+$%.2f", amount)
        } else {
            text = "$—"
        }
        let size: CGFloat
        let color: NSColor
        let weight: NSFont.Weight
        switch tier {
        case "win": (size, color, weight) = (40, Self.gold, .bold)
        case "jackpot": (size, color, weight) = (56, Self.gold, .heavy)
        default: (size, color, weight) = (28, .systemGray, .semibold)
        }

        let layer = CATextLayer()
        layer.string = text
        layer.font = NSFont.monospacedDigitSystemFont(ofSize: size, weight: weight)
        layer.fontSize = size
        layer.alignmentMode = .center
        layer.foregroundColor = color.cgColor
        layer.contentsScale = scale
        if tier == "win" || tier == "jackpot" {
            layer.shadowColor = Self.gold.cgColor
            layer.shadowOpacity = tier == "jackpot" ? 0.95 : 0.8
            layer.shadowRadius = tier == "jackpot" ? 12 : 8
            layer.shadowOffset = .zero
        } else {
            layer.shadowColor = NSColor.black.cgColor
            layer.shadowOpacity = 0.8
            layer.shadowRadius = 2
            layer.shadowOffset = .zero
        }
        let rect = videoRect
        layer.frame = CGRect(x: rect.minX, y: rect.midY - size, width: rect.width, height: size * 1.4)
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        layer.opacity = 0 // model value: invisible outside the animation
        container.addSublayer(layer)
        CATransaction.commit()

        let duration = 1.6
        let fade = CAKeyframeAnimation(keyPath: "opacity")
        fade.values = [0, 1, 1, 0]
        fade.keyTimes = [0, 0.08, 0.62, 1]
        let pop = CAKeyframeAnimation(keyPath: "transform.scale")
        pop.values = tier == "jackpot" ? [0.4, 1.18, 1.0, 1.0] : [0.6, 1.06, 1.0, 1.0]
        pop.keyTimes = [0, 0.12, 0.24, 1]
        let rise = CAKeyframeAnimation(keyPath: "position.y")
        rise.values = [layer.position.y, layer.position.y, layer.position.y + 44]
        rise.keyTimes = [0, 0.55, 1]
        let group = CAAnimationGroup()
        group.animations = [fade, pop, rise]
        group.duration = duration
        layer.add(group, forKey: "flash")
        DispatchQueue.main.asyncAfter(deadline: .now() + duration + 0.1) {
            layer.removeFromSuperlayer()
        }
    }

    /// coinShower rains gold coins from the top edge for a beat. The cell keeps
    /// emitting until the *layer's* birthRate multiplier hits zero — zeroing
    /// only the cell's is the classic eternal-shower bug — and the layer is
    /// removed outright once the last coin has fallen.
    private func coinShower() {
        let rect = videoRect
        let emitter = CAEmitterLayer()
        emitter.frame = container.bounds
        emitter.emitterShape = .line
        emitter.emitterPosition = CGPoint(x: rect.midX, y: rect.maxY + 16)
        emitter.emitterSize = CGSize(width: rect.width, height: 1)

        let cell = CAEmitterCell()
        cell.contents = Self.coinImage
        cell.birthRate = 70
        cell.lifetime = 2.5
        cell.velocity = 320
        cell.velocityRange = 140
        cell.emissionLongitude = -.pi / 2 // straight down in y-up coords
        cell.emissionRange = .pi / 8
        cell.yAcceleration = -600 // gravity pulls toward y=0
        cell.spin = 2
        cell.spinRange = 6
        cell.scale = 0.5
        cell.scaleRange = 0.25
        cell.alphaSpeed = -0.35
        emitter.emitterCells = [cell]
        container.addSublayer(emitter)

        DispatchQueue.main.asyncAfter(deadline: .now() + 1.2) {
            emitter.birthRate = 0
        }
        DispatchQueue.main.asyncAfter(deadline: .now() + 3.5) {
            emitter.removeFromSuperlayer()
        }
    }

    /// coinImage is the emitter's particle: the 🪙 emoji rasterized once — the
    /// app bundles no image assets. It replaced a hand-drawn gold disc with a
    /// $ glyph, which at particle size read as a bitcoin token.
    private static let coinImage: CGImage? = {
        let side: CGFloat = 64
        let image = NSImage(size: NSSize(width: side, height: side), flipped: false) { rect in
            let glyph = NSAttributedString(string: "🪙", attributes: [
                .font: NSFont.systemFont(ofSize: 52),
            ])
            glyph.draw(at: CGPoint(x: rect.midX - glyph.size().width / 2,
                                   y: rect.midY - glyph.size().height / 2))
            return true
        }
        var proposed = CGRect(origin: .zero, size: NSSize(width: side, height: side))
        return image.cgImage(forProposedRect: &proposed, context: nil, hints: nil)
    }()
}

#endif
