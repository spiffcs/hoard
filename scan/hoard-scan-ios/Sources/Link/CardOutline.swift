import CardKit
import SwiftUI

@MainActor
@Observable
final class CardCue {
    var rect: CGRect?
    var phase: TriggerPhase = .off
    var tier: String?
    var resultSequence = 0
    var foilSequence = 0
}

struct CardOutline: View {
    let cue: CardCue
    @State private var flashing = false
    @State private var sheening = false
    @State private var sheenPhase: CGFloat = 0

    var body: some View {
        Group {
            if let rect = cue.rect {
                CornerBrackets(rect: rect)
                    .stroke(color, style: StrokeStyle(
                        lineWidth: lineWidth, lineCap: .round, lineJoin: .round))
                    .shadow(color: .black.opacity(0.5), radius: 2)
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
                    .opacity(pulsing ? 0.55 : 1)
                    .animation(
                        pulsing
                            ? .easeInOut(duration: 0.45).repeatForever(autoreverses: true)
                            : .easeOut(duration: 0.12),
                        value: pulsing)
            }
        }
        .animation(cue.phase == .capturing ? nil : .easeOut(duration: 0.06),
                   value: cue.rect)
        .allowsHitTesting(false)
        .onChange(of: cue.foilSequence) { _, _ in
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
            Task {
                try? await Task.sleep(for: .milliseconds(1600))
                flashing = false
            }
        }
    }

    private func sweep() {
        var instant = Transaction()
        instant.disablesAnimations = true
        withTransaction(instant) { sheenPhase = 0 }
        withAnimation(.easeInOut(duration: 0.7)) { sheenPhase = 1 }
    }

    private var pulsing: Bool { flashing && cue.tier == "jackpot" }

    private var lineWidth: CGFloat { cue.phase == .capturing ? 5 : 3 }

    private func sheenBand(over rect: CGRect) -> some View {
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
            case "jackpot", "big": return Color(red: 1.0, green: 0.84, blue: 0.0)
            case "review": return Color(white: 0.72)
            default: return .green
            }
        }
        return cue.phase == .capturing ? .green : .yellow
    }
}

struct CornerBrackets: Shape {
    var rect: CGRect

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
