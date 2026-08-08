// The price, over the card.
//
// The phone's entire visual job during a session. The terminal has the queue,
// the tally and the review cascade; the phone has the camera and one number,
// big enough to read from arm's length while your hands are full of cards.
//
// One string, tucked under the card rather than laid across it.
//
// It used to be two: a solid number over a much larger translucent copy of
// itself, at 54-71pt in the middle of the frame. The ghost existed to keep the
// number legible over arbitrary card art without a panel behind it. Moving the
// label under the card removes the problem it solved — it sits over the desk
// now — and removing it is most of what stops the price dominating a screen
// whose subject is the card.
//
// Contrast is carried by the strokes that were always there.

import SwiftUI

/// What the Go side decided about a card, as it arrives on the wire.
struct PriceResult: Equatable {
    var amount: Double?
    /// The committed card's name, shown at the bottom of the camera so the
    /// operator reads what registered without glancing at the terminal.
    /// Empty on review results — nothing settled to show.
    var name: String?
    /// bulk | win | jackpot | unpriced | review — decided Go-side, where the
    /// prices already are and where a table test can pin the boundaries. The
    /// phone renders what it is told and never re-derives a tier from a number.
    var tier: String?
    /// The finish that was actually written, from the same side and for the
    /// same reason. "foil" is what lights the sheen.
    var finish: String?

    /// The text on screen. A queued card is not a win yet: the terminal has the
    /// review and the printing — so the price — is still unverified.
    var text: String {
        if tier == "review" { return "Needs Review" }
        guard let amount else { return "$—" }
        return String(format: "$%.2f", amount)
    }

    var color: Color {
        switch tier {
        case "jackpot", "win": return Color(red: 0.30, green: 1.0, blue: 0.35)
        case "review": return .white
        case "unpriced": return .white.opacity(0.85)
        default: return Color(red: 1.0, green: 0.87, blue: 0.20)
        }
    }

    /// Jackpots get more of the screen. Everything else is one size, because a
    /// size that tracks the price makes small numbers hard to read at exactly
    /// the moment there are most of them.
    ///
    /// Roughly half what these were. They were sized to be read at arm's length
    /// as the *only* thing on screen; now the brackets carry "did this go in"
    /// and this only has to carry "how much", from under the card rather than
    /// across it.
    ///
    /// "Needs Review" is the exception, and it is smaller because it is a
    /// phrase rather than a number — twelve characters at the price size is
    /// three times the width of "$46.13" and stops being a label under a card.
    var size: CGFloat {
        switch tier {
        case "jackpot": return 34
        case "review": return 19
        default: return 27
        }
    }

}

/// PriceOverlay shows the last resolved card's price, then fades.
struct PriceOverlay: View {
    let result: PriceResult
    /// Bumped by the caller on every result so the animation restarts even when
    /// two identical prices land in a row — a common case on bulk, and without
    /// this the second one would silently not animate.
    let sequence: Int

    @State private var shown = false
    /// The container, for the centred fallback and the off-screen clamp.
    var size: CGSize = .zero

    /// Where the card is, in this view's coordinates. The label tucks under it
    /// and moves with it, so the frame and the number read as one object.
    var cardRect: CGRect?

    var body: some View {
        Text(result.text)
            .font(.system(size: result.size, weight: .heavy, design: .rounded))
            .foregroundStyle(result.color)
            .lineLimit(1)
            .fixedSize()
            // A dark stroke rather than a shadow: the desk is light and busy at
            // the edges, and a soft shadow disappears against it while an
            // outline holds. These carry the contrast the ghost used to.
            .shadow(color: .black.opacity(0.9), radius: 1, x: 1, y: 1)
            .shadow(color: .black.opacity(0.9), radius: 1, x: -1, y: -1)
            .shadow(color: .black.opacity(0.55), radius: 6)
            .scaleEffect(shown ? 1.0 : (result.tier == "jackpot" ? 0.4 : 0.6))
            .opacity(shown ? 1 : 0)
            .position(anchor)
            // Layout-neutral, deliberately, and this property has to survive
            // the move. The Text is `fixedSize`, so it reports an ideal width
            // far wider than the screen; taking the proposal and clipping keeps
            // a long price string from being able to move anything else.
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .clipped()
            .allowsHitTesting(false)
            .onChange(of: sequence) { _, _ in restart() }
            .onAppear { restart() }
    }

    /// Under the card when there is one, centred when there is not.
    ///
    /// Flipped above the brackets when the card sits low enough that the label
    /// would fall off the bottom — a price half off screen is worse than one on
    /// the wrong side of the card.
    private var anchor: CGPoint {
        guard let card = cardRect, size != .zero else {
            return CGPoint(x: size.width / 2, y: size.height / 2)
        }
        let gap = result.size * 0.9
        let below = card.maxY + gap
        let y = below + result.size < size.height ? below : card.minY - gap
        return CGPoint(x: card.midX, y: y)
    }

    /// The same shape the Mac's HUD uses: a quick pop in, a hold, then a fade.
    /// Timings match so the two sources feel like one product.
    private func restart() {
        shown = false
        withAnimation(.spring(response: 0.22, dampingFraction: 0.62)) { shown = true }
        withAnimation(.easeOut(duration: 0.5).delay(1.1)) { shown = false }
    }
}
