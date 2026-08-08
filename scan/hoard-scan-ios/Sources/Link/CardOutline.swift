// Corner brackets on the card the trigger is watching.
//
// Presentation only. The trigger has already decided by the time any of this
// runs and nothing here can change what gets captured; the job is to make the
// app look calmly locked onto a card, and to make a reluctant trigger
// diagnosable at a glance. Absence is information too — no brackets means the
// detector is finding no rectangle, which is the single most useful thing to
// know when a capture will not fire.
//
// Brackets rather than a full rectangle, and one rather than several. The macOS
// helper learned this the hard way and recorded it: re-fit on every Vision
// sample, full outlines "slid and flickered like the display was struggling",
// and the extra boxes were desk clutter and deck boxes the trigger weighs but
// nobody should have to look at.
//
// What that helper also had to build, and this does not, is stability. It is
// handed raw per-sample boxes, so it re-derives which one to draw (nearest to
// the last) and holds the cue through blinks on a timer. Here the trigger hands
// over `cue` — already identity-matched by IoU, already surviving blinks on
// grace — so this file contains no stabilisation logic at all.

import CardKit
import SwiftUI

/// Where the brackets are and what they mean, as its own observable leaf.
///
/// Deliberately not state on `CameraSession`. That is a `@StateObject` on the
/// session screen, so a rect published there would invalidate the entire view
/// thirty times a second — the camera preview's representable, both of the
/// price's `fixedSize` texts, the header, the footer — to move twelve line
/// segments. Read only inside the overlay's body, this invalidates only the
/// overlay.
@MainActor
@Observable
final class CardCue {
    /// The card, in the preview layer's own coordinates. Nil draws nothing.
    var rect: CGRect?
    var phase: TriggerPhase = .off
    /// The last result's tier, and a sequence that ticks on each new one.
    /// Together they drive the brief flash that says committed or queued.
    var tier: String?
    var resultSequence = 0
    /// Ticks when a capture read a foil marker off the card.
    ///
    /// Separate from `resultSequence` because it is known earlier: the star
    /// between the set code and the language is read on the phone at capture,
    /// roughly half a second before the price comes back from the Mac. The
    /// sheen can start at the shutter rather than waiting for a number.
    ///
    /// It says "this card printed a foil marker", not "this committed as a
    /// foil" — the Mac has the last word on that, since it only accepts the
    /// hint if the printing actually offers that finish. For a cue about what
    /// was *seen*, the phone's own read is the honest source.
    var foilSequence = 0
}

/// CardOutline draws the brackets.
struct CardOutline: View {
    let cue: CardCue
    /// Ticks with `cue.resultSequence` while a result is being shown, so the
    /// flash colour can fade back to the tracking colour on its own.
    @State private var flashing = false
    /// Whether a foil sheen is running, and where the band has travelled to.
    @State private var sheening = false
    @State private var sheenPhase: CGFloat = 0

    var body: some View {
        Group {
            if let rect = cue.rect {
                CornerBrackets(rect: rect)
                    .stroke(color, style: StrokeStyle(
                        lineWidth: lineWidth, lineCap: .round, lineJoin: .round))
                    .shadow(color: .black.opacity(0.5), radius: 2)
                    // The foil sheen: a bright band travelling across the
                    // brackets, masked to the stroke so it lights the marks
                    // themselves rather than the card behind them.
                    //
                    // Motion rather than colour, deliberately. The brackets
                    // already encode committed / review / jackpot in their
                    // colour, and a foil can be any of those — an iridescent
                    // stroke would overwrite exactly that distinction on the
                    // cards where it matters most.
                    .overlay {
                        if sheening {
                            sheenBand(over: rect)
                                .mask {
                                    CornerBrackets(rect: rect)
                                        .stroke(.white, style: StrokeStyle(
                                            lineWidth: lineWidth,
                                            lineCap: .round, lineJoin: .round))
                                }
                                .blendMode(.plusLighter)
                                .allowsHitTesting(false)
                        }
                    }
                    // Gold jackpots breathe; everything else is steady.
                    .opacity(pulsing ? 0.55 : 1)
                    .animation(
                        pulsing
                            ? .easeInOut(duration: 0.45).repeatForever(autoreverses: true)
                            : .easeOut(duration: 0.12),
                        value: pulsing)
            }
        }
        // Position and size ease together; CGRect is not Animatable, so
        // CornerBrackets spells out animatableData over its origin and size.
        //
        // The duration is deliberately shorter than the macOS 0.12s. That value
        // was fitted at a 0.1s sample interval, where the cue moved ten times a
        // second and hopped between candidates. This runs at 0.033s against
        // geometry the trigger has already stabilised, so the sample stream is
        // most of the animation and a long ease mostly adds lag. The shutter
        // stays instant: nothing should trail the moment of capture.
        .animation(cue.phase == .capturing ? nil : .easeOut(duration: 0.06),
                   value: cue.rect)
        .allowsHitTesting(false)
        .onChange(of: cue.foilSequence) { _, _ in
            // Two passes. One reads as a glitch; a slow repeat would keep
            // drawing attention long after the card has been dealt with.
            sheening = true
            sweep()
            Task {
                try? await Task.sleep(for: .milliseconds(750))
                guard sheening else { return }
                sweep()
                try? await Task.sleep(for: .milliseconds(750))
                sheening = false
            }
        }
        .onChange(of: cue.resultSequence) { _, _ in
            flashing = true
            // Matches PriceOverlay's own hold, so the brackets and the number
            // appear and fade as one event rather than two.
            Task {
                try? await Task.sleep(for: .milliseconds(1600))
                flashing = false
            }
        }
    }

    /// One pass of the band across the card.
    private func sweep() {
        // Reset without animation, then animate the travel — otherwise the
        // reset itself animates and the band slides backwards between passes.
        var instant = Transaction()
        instant.disablesAnimations = true
        withTransaction(instant) { sheenPhase = 0 }
        withAnimation(.easeInOut(duration: 0.7)) { sheenPhase = 1 }
    }

    /// Gold is the jackpot celebration. The Mac rains coins from a particle
    /// emitter for this tier; the phone keeps it in the brackets instead, which
    /// is one element carrying the whole story rather than a second overlay
    /// over the card you are trying to look at.
    private var pulsing: Bool { flashing && cue.tier == "jackpot" }

    private var lineWidth: CGFloat { cue.phase == .capturing ? 5 : 3 }

    /// A narrow bright band that travels across the brackets.
    ///
    /// The band is a *fixed* gradient moved by `offset`, not a gradient whose
    /// stops are animated. That distinction is the whole implementation: a
    /// `LinearGradient` is not `Animatable`, so animating its stop locations
    /// swaps one gradient for another instantly and nothing appears to move.
    /// An offset is animatable, so this actually sweeps.
    ///
    /// Diagonal travel, because that is how a foil catches a lamp when tilted.
    private func sheenBand(over rect: CGRect) -> some View {
        // Travel far enough that the band starts and ends fully clear of the
        // card, rather than appearing partway across it.
        let span = rect.width + rect.height
        return LinearGradient(
            stops: [
                .init(color: .white.opacity(0), location: 0),
                .init(color: .white.opacity(0.95), location: 0.5),
                .init(color: .white.opacity(0), location: 1),
            ],
            startPoint: .topLeading, endPoint: .bottomTrailing)
            .frame(width: span * 0.45, height: span * 1.6)
            .rotationEffect(.degrees(35))
            .position(x: rect.minX + span * sheenPhase - span * 0.25, y: rect.midY)
    }

    private var color: Color {
        if flashing {
            switch cue.tier {
            // Big wins wear the gold too; only jackpots breathe. That keeps
            // one glance-distinction per rung: green → gold → pulsing gold.
            case "jackpot", "big": return Color(red: 1.0, green: 0.84, blue: 0.0)
            case "review": return Color(white: 0.72)
            default: return .green
            }
        }
        // Green at the shutter, before any result has come back: the capture
        // itself is worth acknowledging even though the answer is still in
        // flight over the wire.
        return cue.phase == .capturing ? .green : .yellow
    }
}

/// Four L-shaped marks squaring the card's corners.
struct CornerBrackets: Shape {
    var rect: CGRect

    /// `CGRect` does not conform to `Animatable` — `CGPoint` and `CGSize` do —
    /// so the pair is spelled out. Without this the brackets would jump between
    /// positions instead of gliding, since there would be nothing for SwiftUI
    /// to interpolate.
    var animatableData: AnimatablePair<CGPoint.AnimatableData, CGSize.AnimatableData> {
        get { AnimatablePair(rect.origin.animatableData, rect.size.animatableData) }
        set {
            rect.origin.animatableData = newValue.first
            rect.size.animatableData = newValue.second
        }
    }

    func path(in _: CGRect) -> Path {
        var p = Path()
        guard rect.width > 1, rect.height > 1 else { return p }
        // Arms proportional to the card but capped, so a close-up card does not
        // wear giant angles. Straight from the macOS geometry, which is fitted
        // rather than architectural and survives the platform change unchanged.
        let arm = min(min(rect.width, rect.height) * 0.22, 34)

        for (corner, dx, dy) in [
            (CGPoint(x: rect.minX, y: rect.minY), 1.0, 1.0),
            (CGPoint(x: rect.maxX, y: rect.minY), -1.0, 1.0),
            (CGPoint(x: rect.maxX, y: rect.maxY), -1.0, -1.0),
            (CGPoint(x: rect.minX, y: rect.maxY), 1.0, -1.0),
        ] {
            p.move(to: CGPoint(x: corner.x + arm * dx, y: corner.y))
            p.addLine(to: corner)
            p.addLine(to: CGPoint(x: corner.x, y: corner.y + arm * dy))
        }
        return p
    }
}
