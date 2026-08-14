import SwiftUI

struct PriceResult: Equatable {
    var amount: Double?
    var name: String?
    var tier: String?
    var finish: String?

    var text: String {
        if tier == "review" { return "Needs Review" }
        guard let amount else { return "$—" }
        return String(format: "$%.2f", amount)
    }

    var color: Color {
        switch tier {
        case "jackpot", "big", "win": return Color(red: 0.30, green: 1.0, blue: 0.35)
        case "review": return .white
        case "unpriced": return .white.opacity(0.85)
        default: return Color(red: 1.0, green: 0.87, blue: 0.20)
        }
    }

    var size: CGFloat {
        switch tier {
        case "jackpot": return 34
        case "big": return 30
        case "review": return 19
        default: return 27
        }
    }

}

struct PriceOverlay: View {
    let result: PriceResult
    let sequence: Int

    @State private var shown = false
    var size: CGSize = .zero

    var cardRect: CGRect?

    var body: some View {
        Text(result.text)
            .font(.system(size: result.size, weight: .heavy, design: .rounded))
            .foregroundStyle(result.color)
            .lineLimit(1)
            .fixedSize()
            .shadow(color: .black.opacity(0.9), radius: 1, x: 1, y: 1)
            .shadow(color: .black.opacity(0.9), radius: 1, x: -1, y: -1)
            .shadow(color: .black.opacity(0.55), radius: 6)
            .scaleEffect(shown ? 1.0 : (result.tier == "jackpot" ? 0.4 : 0.6))
            .opacity(shown ? 1 : 0)
            .position(anchor)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .clipped()
            .allowsHitTesting(false)
            .onChange(of: sequence) { _, _ in restart() }
            .onAppear { restart() }
    }

    private var anchor: CGPoint {
        guard let card = cardRect, size != .zero else {
            return CGPoint(x: size.width / 2, y: size.height / 2)
        }
        let gap = result.size * 0.9
        let below = card.maxY + gap
        let y = below + result.size < size.height ? below : card.minY - gap
        return CGPoint(x: card.midX, y: y)
    }

    private func restart() {
        shown = false
        withAnimation(.spring(response: 0.22, dampingFraction: 0.62)) { shown = true }
        withAnimation(.easeOut(duration: 0.5).delay(1.1)) { shown = false }
    }
}
