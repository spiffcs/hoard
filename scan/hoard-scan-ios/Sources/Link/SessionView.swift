// A scanning session, as the phone sees it.
//
// The camera, one number, and a pairing code until the Mac connects. Everything
// else — the queue, the tally, the review cascade, the running total — is in the
// terminal, because that is where the person's attention is once the rig is set
// up and their hands are full of cards.
//
// The phone is deliberately not a second control surface. There is no shutter
// button here: the terminal drives, exactly as it does for the Continuity
// Camera path, and a phone that also captured would be a third place to drive
// the session from.

import CardKit
import ScanWire
import SwiftUI

struct SessionView: View {
    /// Owned by the app so the phone advertises whenever it is open, not only
    /// while this tab is on screen. See HoardScanApp.
    @ObservedObject var link: LinkController

    @StateObject private var camera = CameraSession()
    @State private var busy = false
    @State private var lastRead = ""
    @State private var autoPhase: TriggerPhase = .off
    /// The brackets' own observable leaf. Held here but never read in this
    /// body, so a rect changing thirty times a second invalidates the overlay
    /// and nothing else.
    @State private var cue = CardCue()
    /// Converts a Vision box into preview-layer coordinates. Handed over by the
    /// preview once its layer exists, because only the layer knows the gravity
    /// and rotation involved.
    @State private var toLayer: ((CGRect) -> CGRect)?
    /// Set on the Pair tab. Gates the two readouts that are an account of the
    /// machinery rather than an answer about a card.
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
            // As an overlay on the fitted preview, not a ZStack sibling: the
            // converted rect is in the preview layer's coordinates, and the
            // other children of the ZStack fill the whole screen rather than
            // the letterboxed picture. A sibling would be off by the letterbox.
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

            // Only once a card has actually resolved. Rendering the empty
            // result on appear flashed a neutral "$—" at launch, which reads as
            // the app having scanned something before the camera was even up.

            VStack {
                header
                Spacer()
                footer
            }
        }
        // The result flash is driven by a result arriving, not by the trigger's
        // hold phase. Hold runs until the card is disturbed, which can be many
        // seconds; the flash is a moment. Keeping them separate is what makes
        // "brief flash, then clear" possible without touching the trigger.
        .onChange(of: link.priceSequence) { _, seq in
            cue.tier = link.price.tier
            cue.resultSequence = seq
            // The sheen rides the result rather than the capture's own read.
            //
            // It first fired off `reading.printing.finish`, which is only
            // whether a star was seen between the set code and the language —
            // half a second earlier, but a weaker claim. The parent has the
            // catalog: it knows whether the printing comes in foil at all, and
            // what it therefore wrote down. A cue about what was recorded
            // should follow what was recorded.
            if link.price.finish == "foil" {
                cue.foilSequence += 1
            }
        }
        .statusBarHidden()
        .task {
            await camera.start()
            // Only the capture hook is wired here: the camera lives on this
            // screen, so this is the one thing the link cannot do without it.
            // Starting and stopping the listener is the app's job.
            // Advertised only once the camera is up, so the feature list
            // reflects whether a video tap actually attached rather than
            // promising a trigger that can never fire.
            link.setAutoAvailable(camera.autoAvailable)
            link.onCapture = { shoot(auto: false) }
            link.onAuto = { on in
                if on { camera.startTrigger() } else { camera.stopTrigger() }
            }
            link.onRearm = { camera.nudgeTrigger() }
            link.onTorch = { on in camera.setTorch(on ? 1 : 0) }
            camera.onFire = { shoot(auto: true) }
            camera.onTriggerTrace = { link.trace($0) }
            // The brackets follow the trigger's own cue, converted into the
            // preview's coordinates by the layer that owns them.
            camera.onCue = { box in
                cue.rect = box.flatMap { b in toLayer.map { $0(b) } }
            }
            link.onTune = { camera.tuneTrigger(stable: $0, interval: $1) }
            camera.onPhase = { phase in
                autoPhase = phase
                cue.phase = phase
                // Only settled phases go on the wire; searching↔stabilizing
                // flapping is a visual thing, not a protocol event.
                let wire: String
                switch phase {
                case .searching, .stabilizing: wire = "armed"
                case .capturing: wire = "capturing"
                case .hold: wire = "held"
                case .off: wire = "off"
                }
                link.sendAuto(state: wire)
            }
            // Armed last, once every handler above is in place: the trigger can
            // fire on its first sample, and a fire that arrives before onFire
            // is assigned is simply lost.
            //
            // On by default, rather than waiting to be told. It is what the rig
            // is for — a phone in a stand exists so cards can be put down
            // instead of shutters pressed — and the terminal's auto-on can
            // arrive before this screen has finished setting up, landing on a
            // nil handler and being dropped. Same race as the ready event, and
            // the same fix: do not depend on a message whose timing is not ours.
            camera.startTrigger()
            // The rig is a phone in a stand for hours; letting it sleep
            // mid-box would end the session silently.
            UIApplication.shared.isIdleTimerDisabled = true
        }
    }

    /// Until the Mac connects, the code is the only thing on screen that
    /// matters, so it is large and unmissable. After that it is out of the way.
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
            // Not connected. The code itself lives on the Pair tab: it is
            // needed once per Mac and then never again, so keeping it here
            // would put a six-digit secret on screen for every card scanned
            // afterwards.
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

    private var footer: some View {
        VStack(spacing: 6) {
            // Behind developer mode, both of the lines this footer used to show
            // unconditionally.
            //
            // "Sol Ring  LEA 270" is a check on the reader, and "(nothing read)"
            // is the same check reporting a miss — neither is something to do
            // anything about mid-box, and the miss in particular reads as an
            // error when it is usually just a card halfway onto the mat. The
            // read goes to the wire and to SessionLog either way, so nothing is
            // lost by not saying it here.
            if developerMode, !lastRead.isEmpty {
                Text(lastRead).font(.caption2.monospaced()).lineLimit(1)
            }
            // The per-capture timing used to sit here — "163ms shutter · 299ms
            // read · 4032x3024" — on the reasoning that tuning the rig means
            // watching it change while moving the phone, and that a log on
            // another machine is not something anyone reads while holding a
            // card. That reasoning was sound when this was the only copy.
            //
            // It no longer is: the same line goes to the wire as a trace and to
            // SessionLog, which the Pair tab can share off the device. So what
            // is left on the scanning screen is a developer's readout on a
            // surface whose whole job is a price, in an app whose whole job is
            // capture.
            // Audio can be silently silent in several ways — muted device,
            // failed engine, wrong session category — and every one of them
            // looks like "the Mac never sent a result". Say which.
            if !link.sounds.isWorking {
                Label(link.sounds.status, systemImage: "speaker.slash.fill")
                    .font(.caption2).foregroundStyle(.orange)
            }
            // Hands-free, by hand. The terminal arms this on connect, but a
            // toggle on the phone is the difference between "it is not working"
            // and knowing which half is not working.
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
            // The trigger's running commentary — "Waiting for a card",
            // "Holding still…", "Focused · tap to focus" — is a window onto the
            // state machine, and watching it is tuning work. A camera that
            // never opened is not: that line stays, in the colour the sound
            // warning above uses, because it is the only explanation the person
            // holding the phone would get for a preview that never appears.
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

    /// What the trigger is doing, in words for someone glancing at the phone.
    private var autoStatus: String {
        guard camera.locked else { return camera.status + " · tap to focus" }
        switch autoPhase {
        // No lens position. It is a normalised float nobody holding cards can
        // act on, and it reads as a debug overlay left switched on.
        case .off: return "Manual"
        case .searching: return "Waiting for a card"
        case .stabilizing: return "Holding still…"
        case .capturing: return "Captured"
        case .hold: return "Swap in the next card"
        }
    }

    /// shoot captures, reads, and reports. The read happens here rather than on
    /// the Mac because the pixels are here — a 24 MP still crossing the wire per
    /// card would cost more than the whole read does.
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
            // The trigger's own account of why it fired travels with the read.
            // A manual shutter sends none: nobody watched a card arrive, the
            // operator simply pressed the button.
            // The measurements ride the same gate as the cause for the same
            // reason: on a manual shutter there was no trigger decision, so
            // there are no numbers behind one either.
            link.sendScan(reading, rotation: 0, auto: auto,
                          fireReason: auto ? camera.fireCause : nil,
                          holdDelta: auto ? camera.fireHoldDelta : nil,
                          faceDelta: auto ? camera.fireFaceDelta : nil)

            // Every leg of the loop, in the shape the macOS helper's timing
            // line already has, so HOARD_SCAN_LOG reads the same whichever
            // source produced it. The budget to beat is the ledger's ~0.7s from
            // shutter to result; anything longer and the rhythm stops being
            // "silence then payoff" and starts being "did it hear me".
            //
            // The tap size is here because nothing else reports it, and it
            // decides whether the trigger is being fed what it was tuned for.
            let ms = { (a: Date, b: Date) in Int(b.timeIntervalSince(a) * 1000) }
            link.trace(
                "timing shutter=\(ms(started, shutter))ms"
                + " read=\(ms(shutter, read))ms"
                + " total=\(ms(started, read))ms"
                + " still=\(frame.image.width)x\(frame.image.height)"
                + " tap=\(Int(camera.tapSize.width))x\(Int(camera.tapSize.height))"
                // The read's own stages. `read` alone says the pipeline is slow
                // without saying where, and the three stages are tuned
                // independently — locate is segmentation plus the perspective
                // flatten, whole and band are the two text passes, which run
                // concurrently, so the read is roughly locate + the larger.
                + " \(reading.timings.line)"
                // The border read, every time, verdict or not. This is the
                // measurement that decides whether it may ever settle a
                // printing, and the corpus cannot supply it: those images are
                // scans in which the card fills the frame, so segmentation has
                // no background to lock onto and trims the border away on
                // roughly half of them. A live pile is the only source of the
                // geometry this reader will actually see.
                // `abstain` names the gate that refused, always. The reader
                // this replaced reported "between tones" for every failure
                // except ring uniformity, so two white cards that were actually
                // rejected on the standoff check read as a tone problem — and
                // the tuning that followed went looking in the wrong place.
                + " border=\(reading.border.color ?? "-")"
                + "(\(reading.border.source ?? reading.border.abstain))"
                + String(format: " t=%.2f standoff=%.2f anchor=%@ scale=%.2f",
                         reading.border.t, reading.border.standoff,
                         reading.border.anchorKind, reading.border.scaleAgreement))
            // Hand the clock to the link so the round trip back — resolve on the
            // Mac, price returned — can be closed out when the result lands.
            link.markCaptureSent(at: started, localMS: ms(started, read))
            // A capture that read something proves the lens is where the cards
            // are; one that read nothing counts toward thawing it. This is what
            // makes locking safe — without it a lens locked on an empty desk
            // stays there and every card afterwards is soft.
            // After the read and after the event: a multi-megabyte frame must
            // never sit in front of the result the operator is waiting to hear.
            link.sendStill(frame.image)
            camera.focus(afterGoodRead: !reading.title.isEmpty)
            lastRead = reading.title.isEmpty
                ? "(nothing read)"
                : "\(reading.title)  \(reading.printing.setCode) \(reading.printing.number)"
            // Park the trigger on what was just shot, whatever fired it. A
            // manual shutter must do this too or the trigger can double-fire
            // on the same card.
            camera.triggerCaptureFinished()
        }
    }
}
