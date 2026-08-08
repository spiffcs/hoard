// macOS only. ScanKit is the Mac end of the link: the translator, and the
// optional mirror window it can draw. See Package.swift.
#if os(macOS)

import AppKit
import Foundation
import ScanLink
import ScanWire

/// RemoteController is the scan session. The camera is an iPhone running the
/// companion app, reached over the network.
///
/// It owns no camera, no trigger and no read pipeline — the phone has all
/// three. What it does is translate: frames from the link become NDJSON on
/// stdout, stdin verbs become frames on the link, and the Go side talks to a
/// pipe. That is the whole design, and it is why every capability the parent
/// negotiates arrives through the `ready` event's feature list rather than
/// being assumed here.
///
/// **Headless.** The phone shows the preview, the price and the outline cue,
/// and plays the sounds; the terminal shows the queue. There is nothing for a
/// Mac window to add, and one would be a second place to look during a
/// session where the user is already looking at two. (A `--mirror` window
/// existed once and was deleted: no preview frame was ever sent, so it shipped
/// as a permanently black rectangle. Resurrect it only with the phone-side
/// sender in the same change.)
final class RemoteController: NSObject {
    private let browser = PeerBrowser()
    private let requestedDevice: String?
    private var session: PeerSession?

    /// The pairing code, remembered between runs by the Go side and handed back
    /// on the command line. A session with no code cannot pair, and saying so
    /// up front is better than a browse that finds a phone and then silently
    /// fails its handshake.
    private let code: PairingCode

    /// Verify mode: connect, wait for the phone to say `ready`, and exit. No
    /// session, no window, no camera work.
    ///
    /// It exists so that "paired" can mean "this works" rather than "six digits
    /// were written to a file". Without it a mistyped code is accepted happily
    /// and fails much later, at the start of a scanning session, looking like a
    /// network fault rather than a typo.
    private let verifyOnly: Bool

    init(deviceID: String?, code: PairingCode, verifyOnly: Bool = false) {
        self.requestedDevice = deviceID
        self.code = code
        self.verifyOnly = verifyOnly
        super.init()
    }

    func start() {
        // A phone a beat slow to advertise is not an absent phone, so this
        // waits rather than concluding on the first empty answer.
        let services = browser.browse(seconds: phoneWait)
        let chosen = requestedDevice.flatMap { id in services.first { $0.id == id } }
            ?? services.first
        guard let chosen else {
            fail(noPhoneAppMessage, code: 4)
        }

        let session = browser.connect(to: chosen, code: code)
        self.session = session

        if verifyOnly {
            verify(session, chosenName: chosen.name)  // never returns
        }

        wire(session)

        // Readiness is the phone's to declare: it knows whether its camera came
        // up, and the feature list it sends is what the parent negotiates
        // against. Nothing is announced here — a `ready` synthesised on this
        // side would promise capabilities the phone may not have.
    }

    /// wire connects the link to stdout and back.
    private func wire(_ session: PeerSession) {
        session.control.onFrame = { [weak self] frame in
            DispatchQueue.main.async { self?.handle(frame) }
        }
        session.control.onState = { [weak self] state in
            DispatchQueue.main.async { self?.linkStateChanged(state) }
        }
        session.preview.onFrame = { [weak self] frame in
            // Everything goes through the same handler the control connection
            // uses. Fixture stills travel on this connection on purpose — it
            // exists to carry large droppable payloads, and a multi-megabyte
            // JPEG queued ahead of a `result` on the control connection would
            // delay the sound the operator is waiting for. This callback used
            // to drop non-preview frames on the floor, so a still sent here
            // arrived and vanished with no error on either side.
            DispatchQueue.main.async { self?.handle(frame) }
        }
        session.preview.onState = { [weak self] state in
            // Same treatment as the control leg. A preview connection that
            // dies while control survives is a session that keeps scanning
            // while every fixture still vanishes into a dead socket — no
            // error on either side, and a corpus session discovers its loss
            // only when the debug dir comes up empty.
            DispatchQueue.main.async { self?.linkStateChanged(state) }
        }
    }

    /// verify waits for the phone to say `ready`, then exits.
    ///
    /// The phone only sends `ready` after the pairing check passes, so seeing it
    /// *is* the verification — a wrong code fails the hello and nothing ever
    /// arrives, which is why this has a deadline rather than patience.
    ///
    /// The flag is set from the link's own queue and polled here, deliberately.
    /// The obvious version hops to the main queue to record it, and that
    /// deadlocks: this runs inside a block already executing on the main queue,
    /// and the main queue is serial, so a block dispatched to it cannot run
    /// until this one returns — no matter how hard the run loop is pumped.
    /// Measured as a pairing that always timed out while the phone sat there
    /// showing "connected to hoard".
    private func verify(_ session: PeerSession, chosenName: String) -> Never {
        let ready = Flag()
        session.control.onFrame = { frame in
            guard frame.kind == .ndjson, let text = frame.text,
                  text.contains("\"event\":\"ready\"")
            else { return }
            ready.set()
        }
        spinRunLoop(seconds: verifyTimeout) { ready.isSet }
        let ok = ready.isSet
        session.cancel()
        if !ok {
            fail("The phone did not accept that code. Check the digits on its Pair tab",
                 code: 5)
        }
        // Say so on stdout. runHelper treats an empty stdout as a hard failure
        // and reports whatever is on stderr instead — so a verification that
        // succeeded silently came back as "scan helper produced no output",
        // which is both wrong and impossible to act on.
        emit(Event(event: "ready", device: chosenName))
        exit(0)
    }

    private func handle(_ frame: Frame) {
        switch frame.kind {
        case .ndjson:
            // Passed through undecoded rather than decoded and re-encoded. The
            // phone speaks ScanWire's Event, this side speaks it too, and a
            // round trip through a struct here would silently drop any field
            // this build happens not to know about — which is exactly the
            // forward compatibility the wire contract promises.
            //
            // Not quite verbatim, though: the frame codec explicitly permits
            // newlines in a payload, but stdout here is the Go side's *line*
            // parser, so a raw \n or \r inside the payload would split one
            // event into two half-lines and poison everything after it.
            // Interior newline bytes become spaces — a legal JSON encoder
            // never emits them (it writes the two-byte escape \n instead), so
            // this only ever rewrites a payload that was already going to
            // break the pipe.
            var line = frame.payload
            for i in line.indices where line[i] == 0x0A || line[i] == 0x0D {
                line[i] = 0x20
            }
            FileHandle.standardOutput.write(line)
            FileHandle.standardOutput.write(Data("\n".utf8))
            // The phone declares readiness, so this is the first moment it is
            // known to be listening. Asking earlier would talk to a link that
            // has not finished coming up.
            if let text = frame.text, text.contains("\"event\":\"ready\"") {
                // The helper's own build stamp, beside the phone's (which
                // rides inside the ready event itself) — so one session log
                // names every build in the chain.
                let build = Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion")
                    as? String ?? "unstamped"
                FileHandle.standardError.write(Data("scan: helper build \(build)\n".utf8))
                requestStillsIfWanted()
                sendTuningIfSet()
            }
        case .trace:
            // Re-emitted on stderr so HOARD_SCAN_LOG telemetry stays whole
            // across the hop. Without this the phone's timing and trigger lines
            // simply vanish, and those are the tuning loop.
            if let text = frame.text {
                FileHandle.standardError.write(Data("scan: \(text)\n".utf8))
            }
        case .still:
            saveRemoteStill(frame.payload)
        case .preview:
            // Nothing sends these since the mirror window was deleted; the
            // kind stays in the wire contract for older phone builds.
            break
        }
    }

    /// requestStillsIfWanted asks the phone for its raw captures when the
    /// parent has somewhere to put them.
    ///
    /// Tied to HOARD_SCAN_DEBUG_DIR rather than a flag of its own, because that
    /// variable already means "I am building a fixture set" everywhere else in
    /// this helper, and a second way to say the same thing is a second way to
    /// forget. Off otherwise: these are 4032x3024 JPEGs, one per card, and not
    /// shipping them is most of the point of reading on the phone.
    /// sendTuningIfSet forwards the trigger knobs the session was started with.
    ///
    /// The same two variables the local helper already honours, so one command
    /// line tunes whichever source is in use and a session's telemetry can be
    /// read against the values that produced it.
    private func sendTuningIfSet() {
        let env = ProcessInfo.processInfo.environment
        let stable = env["HOARD_SCAN_AUTO_STABLE"].flatMap(Int.init)
        let interval = env["HOARD_SCAN_AUTO_INTERVAL"].flatMap(Double.init)
        guard stable != nil || interval != nil else { return }
        handle(command: "tune \(stable ?? 6) \(interval ?? 0.1)")
    }

    private func requestStillsIfWanted() {
        guard ProcessInfo.processInfo.environment["HOARD_SCAN_DEBUG_DIR"] != nil
        else { return }
        handle(command: "stills-on")
    }

    private func linkStateChanged(_ state: PeerState) {
        switch state {
        case .failed(let failure):
            // An `error`, never a `closed`. `closed` tears down the add session
            // the user is in the middle of; a phone that walked out of range is
            // a problem to report, not a reason to throw away a review queue.
            //
            // Two registers: the terminal gets the sentence that says what to
            // do, and the framework's own words go to stderr where the rest of
            // the session's diagnostics already live. Sending the raw text to
            // the TUI put "(Network.NWError error 53 - Software caused
            // connection abort)" in front of someone mid-pile, which names an
            // errno and not one thing they could act on.
            FileHandle.standardError.write(
                Data("scan: link failed: \(failure.detail)\n".utf8))
            emit(Event(event: "error", message: failure.reason))
        case .cancelled:
            emit(Event(event: "closed"))
            exit(0)
        default:
            break
        }
    }

    // MARK: - Commands from the parent

    func handle(command: String) {
        guard let verb = ScanCommand(line: command) else {
            emit(Event(event: "error", message: "Unknown command: \(command)"))
            return
        }
        if case .quit = verb {
            shutdown()
            return
        }
        // Everything else is the phone's to act on. Forwarded as the same line
        // the parent sent, so a verb this build has never heard of still
        // reaches a phone that understands it.
        guard let session else { return }
        session.control.send(Frame(kind: .ndjson, payload: Data(command.utf8)))
    }

    func shutdown() {
        session?.cancel()
        emit(Event(event: "closed"))
        exit(0)
    }

}

/// The `kind` a network source carries in the device list. The Go side matches
/// on this to decide whether a chosen device needs a pairing code, so it is a
/// contract rather than a label — changing it silently breaks the picker.
public let remoteKind = "Hoardling"

/// How long a pairing check waits for the phone to answer. Long enough for a
/// handshake on a slow network, short enough that a wrong code fails while the
/// user is still looking at the screen.
let verifyTimeout = 6.0

/// How long to wait for the chosen phone to advertise itself when opening a
/// session. Override with HOARD_SCAN_WAIT (seconds) on a slow network.
let phoneWait = ProcessInfo.processInfo.environment["HOARD_SCAN_WAIT"]
    .flatMap(Double.init) ?? 2.5

/// How long to browse for phones in `--list-devices`. Shorter than `phoneWait`:
/// Bonjour on a local network answers fast, and this cost is paid every time
/// the picker opens, whereas opening a session is a once-per-run patience.
let remoteBrowseWait = 1.5

/// noPhoneAppMessage is the one place the "open the app" guidance is worded.
let noPhoneAppMessage = """
no iPhone running Hoardling was found on this network. Open the app on your \
phone, make sure both devices are on the same Wi-Fi (or the phone is plugged \
in), and check the pairing code matches.
"""

#endif
