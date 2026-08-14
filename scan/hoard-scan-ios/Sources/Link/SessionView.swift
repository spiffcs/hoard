import CardKit
import ScanWire
import SwiftUI

struct SessionView: View {
    @ObservedObject var link: LinkController

    @StateObject private var camera = CameraSession()
    @State private var busy = false
    @State private var lastRead = ""
    @State private var autoPhase: TriggerPhase = .off
    @State private var cue = CardCue()
    @State private var toLayer: ((CGRect) -> CGRect)?
    @AppStorage(DevMode.key) private var developerMode = false

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()

            PreviewLayerView(
                session: camera.session,
                onConnection: { camera.previewConnection = $0 },
                onConverter: { toLayer = $0 },
                onTap: { camera.refocus(at: $0) })
            .aspectRatio(camera.previewAspect, contentMode: .fit)
            .overlay {
                GeometryReader { geo in
                    ZStack {
                        CardOutline(cue: cue)
                        if link.priceSequence > 0 {
                            PriceOverlay(result: link.price,
                                         sequence: link.priceSequence,
                                         size: geo.size,
                                         cardRect: cue.rect)
                        }
                    }
                }
            }

            VStack {
                header
                Spacer()
                if let offer = link.dupOffer {
                    dupOfferBanner(offer)
                }
                footer
            }
            .animation(.easeInOut(duration: 0.2), value: link.dupOffer)
        }
        .onChange(of: link.priceSequence) { _, seq in
            cue.tier = link.price.tier
            cue.resultSequence = seq
            if link.price.finish == "foil" {
                cue.foilSequence += 1
            }
        }
        .statusBarHidden()
        .onChange(of: link.connected) { _, connected in
            if connected {
                camera.stopTrigger()
            } else {
                camera.startTrigger()
            }
        }
        .task {
            await camera.start()
            link.setAutoAvailable(camera.autoAvailable)
            link.onCapture = { shoot(auto: false) }
            link.onAuto = { on in
                if on { camera.startTrigger() } else { camera.stopTrigger() }
            }
            link.onRearm = { camera.nudgeTrigger() }
            link.onResult = { camera.rearmForResult() }
            link.onEVBias = { camera.setOneShotEVBias($0) }
            link.onTorch = { on in camera.setTorch(on ? 1 : 0) }
            camera.onFire = { shoot(auto: true) }
            camera.onTriggerTrace = { link.trace($0) }
            camera.onCue = { box in
                cue.rect = box.flatMap { b in toLayer.map { $0(b) } }
            }
            link.onTune = { camera.tuneTrigger(stable: $0, interval: $1) }
            if !link.connected { camera.startTrigger() }
            camera.onPhase = { phase in
                autoPhase = phase
                cue.phase = phase
                let wire: String
                switch phase {
                case .searching, .stabilizing: wire = "armed"
                case .capturing: wire = "capturing"
                case .hold: wire = "held"
                case .off: wire = "off"
                }
                link.sendAuto(state: wire)
            }
            camera.startTrigger()
            UIApplication.shared.isIdleTimerDisabled = true
        }
        .onDisappear {
            camera.stop()
            link.onCapture = nil
            link.onAuto = nil
            link.onRearm = nil
            link.onResult = nil
            link.onEVBias = nil
            link.onTorch = nil
            link.onTune = nil
            UIApplication.shared.isIdleTimerDisabled = false
        }
    }

    @ViewBuilder private var header: some View {
        if link.connected {
            HStack(spacing: 6) {
                Circle().fill(.green).frame(width: 8, height: 8)
                Text("hoard connected").font(.caption)
                Spacer()
                if busy { ProgressView().controlSize(.mini) }
            }
            .foregroundStyle(.white.opacity(0.75))
            .padding(.horizontal)
            .padding(.top, 8)
        } else {
            HStack(spacing: 6) {
                Circle().fill(.orange).frame(width: 8, height: 8)
                Text("Not connected").font(.caption)
                Spacer()
            }
            .foregroundStyle(.white.opacity(0.75))
            .padding(.horizontal)
            .padding(.top, 8)
        }
    }

    private func dupOfferBanner(_ text: String) -> some View {
        HStack(spacing: 10) {
            Text(text)
                .font(.callout)
                .lineLimit(2)
                .fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: 0)
            Button {
                link.promote()
            } label: {
                Label("Second copy", systemImage: "plus.circle.fill")
                    .font(.callout.bold())
            }
            .buttonStyle(.borderedProminent)
            Button {
                link.dismissDupOffer()
            } label: {
                Image(systemName: "xmark")
                    .font(.callout.bold())
                    .padding(8)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .foregroundStyle(.secondary)
            .accessibilityLabel("Dismiss")
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
        .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 12))
        .padding(.horizontal, 12)
        .transition(.move(edge: .bottom).combined(with: .opacity))
    }

    private var footer: some View {
        VStack(spacing: 6) {
            if !link.connected, !lastRead.isEmpty {
                Text(lastRead)
                    .font(.callout.weight(.semibold).monospaced())
                    .lineLimit(2)
                    .multilineTextAlignment(.center)
                    .foregroundStyle(.white)
                    .padding(.horizontal)
            } else if developerMode, !lastRead.isEmpty {
                Text(lastRead).font(.caption2.monospaced()).lineLimit(1)
            }
            if !link.sounds.isWorking {
                Label(link.sounds.status, systemImage: "speaker.slash.fill")
                    .font(.caption2).foregroundStyle(.orange)
            }
            if let name = link.price.name, !name.isEmpty {
                Text(name)
                    .font(.callout.weight(.semibold))
                    .lineLimit(1)
                    .padding(.horizontal, 10).padding(.vertical, 3)
                    .background(.black.opacity(0.55), in: Capsule())
                    .foregroundStyle(.white)
            }
            HStack(spacing: 10) {
                if camera.autoAvailable {
                    Button(camera.autoOn ? "Auto: on" : "Auto: off") { camera.toggleAuto() }
                        .font(.caption2)
                        .buttonStyle(.borderedProminent)
                        .tint(camera.autoOn ? .green : .gray)
                } else {
                    Label(camera.autoUnavailableReason.isEmpty
                        ? "Hands-free unavailable" : camera.autoUnavailableReason,
                        systemImage: "exclamationmark.triangle.fill")
                        .font(.caption2).foregroundStyle(.orange)
                }
            }
            if developerMode {
                Text(autoStatus)
                    .font(.caption2)
            } else if !camera.failure.isEmpty {
                Label(camera.failure, systemImage: "exclamationmark.triangle.fill")
                    .font(.caption2).foregroundStyle(.orange)
            }
        }
        .foregroundStyle(.white.opacity(0.6))
        .padding(.bottom, 10)
    }

    private var autoStatus: String {
        guard camera.locked else { return camera.status + " · tap to focus" }
        switch autoPhase {
        case .off: return "Manual"
        case .searching: return "Waiting for a card"
        case .stabilizing: return "Holding still…"
        case .capturing: return "Captured"
        case .hold: return "Swap in the next card"
        }
    }

    private func shoot(auto: Bool) {
        guard !busy else { return }
        busy = true
        Task {
            defer { busy = false }
            let started = Date()
            camera.triggerCaptureBegan()
            guard let frame = await camera.capture() else {
                link.send(Event(event: "error", message: "capture failed"))
                return
            }
            let shutter = Date()
            guard #available(iOS 18, *) else { return }
            let reading = await readCard(uprighted(frame.image, frame.orientation))
            let read = Date()
            link.sendScan(reading, rotation: 0, auto: auto,
                          fireReason: auto ? camera.fireCause : nil,
                          holdDelta: auto ? camera.fireHoldDelta : nil,
                          faceDelta: auto ? camera.fireFaceDelta : nil)

            let ms = { (a: Date, b: Date) in Int(b.timeIntervalSince(a) * 1000) }
            link.trace(
                "timing shutter=\(ms(started, shutter))ms"
                + " read=\(ms(shutter, read))ms"
                + " total=\(ms(started, read))ms"
                + " still=\(frame.image.width)x\(frame.image.height)"
                + " tap=\(Int(camera.tapSize.width))x\(Int(camera.tapSize.height))"
                + " \(reading.timings.line)"
                + " border=\(reading.border.color ?? "-")"
                + "(\(reading.border.source ?? reading.border.abstain))"
                + String(format: " t=%.2f standoff=%.2f anchor=%@ scale=%.2f",
                         reading.border.t, reading.border.standoff,
                         reading.border.anchorKind, reading.border.scaleAgreement))
            link.markCaptureSent(at: started, localMS: ms(started, read))
            link.sendStill(frame.image)
            camera.focus(afterGoodRead: !reading.title.isEmpty)
            let printing = reading.printing
            var mark = ""
            if printing.finishSource == "separator" {
                switch printing.finish {
                case "foil": mark = "★"
                case "nonfoil": mark = "•"
                default: break
                }
            }
            let detail = [printing.setCode, printing.number, mark]
                .filter { !$0.isEmpty }
                .joined(separator: " ")
            lastRead = reading.title.isEmpty
                ? "(nothing read)"
                : detail.isEmpty ? reading.title : "\(reading.title)  \(detail)"
            camera.triggerCaptureFinished()
        }
    }
}
