// hoard-scan — the Mac's half of the iPhone card scanner.
//
// This process owns no camera. The phone running Hoardling is the
// camera: it captures, reads the card with CardKit, and sends finished events
// over the network. What happens here is translation — frames from the link
// become NDJSON on stdout, stdin verbs become frames on the link — so the Go
// side talks to a pipe and never learns where the pixels came from.
//
// Modes:
//   hoard-scan --list-devices   Print the phones visible on the network as JSON, exit.
//   hoard-scan --code <digits> [--device <id>] [--verify]
//                               Live session against a paired iPhone. One JSON
//                               event per line on stdout, commands (capture /
//                               auto-on / auto-off / rearm / torch-on /
//                               torch-off / chime / result {json} / quit) as
//                               lines on stdin.
//
//                               Headless, always: the phone shows the preview,
//                               the price and the cue, and plays the sounds.
//
//                               --verify connects, waits for the phone to accept
//                               the code, and exits — that is what makes
//                               "paired" mean "this works".
//
// `--remote` is accepted and ignored. It used to select this backend over a
// local Continuity Camera; the Continuity path is gone, so there is nothing
// left to select, but the Go side still passes the flag so that a new hoard
// driving an older helper still lands here rather than opening a camera.
//
// Output is newline-delimited JSON; see ScanWire's Event for the shapes.
// For --list-devices: {"devices": [{"id": "...", "name": "...", "kind": "..."}]}
//
// The Go side (internal/scan) manages this process and parses those events.
// Reading a card from an image file is cardkit-probe's job, not this one's —
// see scan/fixtures/sweep.sh.

// macOS only. ScanKit is the Mac end of the link: the translator, and the
// optional mirror window it can draw. See Package.swift.
#if os(macOS)

import AppKit
import ScanLink
import ScanWire

/// ScanArgs is the command line, parsed and nothing else.
///
/// Split out from `runCLI` so it can be tested: everything around it browses
/// the network, takes over the process with `NSApplication.run()`, or calls
/// `exit`, none of which a test bundle can do. The parse is the part that has
/// edge cases — a `--device` with nothing after it, a code that is not six
/// digits — and it was the part with no coverage.
struct ScanArgs {
    var listDevices = false
    var device: String?
    /// nil when `--code` is absent, present but last, or not six digits. The
    /// three cases collapse on purpose: the caller's message is the same, and
    /// naming which one it was would be guidance nobody can act on differently.
    var code: PairingCode?
    var verify = false

    init(_ args: [String]) {
        listDevices = args.contains("--list-devices")
        verify = args.contains("--verify")
        // `i + 1 < args.count` rather than a plain successor: `--device` as the
        // final argument is a typo, not a request to read past the end.
        if let i = args.firstIndex(of: "--device"), i + 1 < args.count {
            device = args[i + 1]
        }
        if let i = args.firstIndex(of: "--code"), i + 1 < args.count {
            code = PairingCode(args[i + 1])
        }
        // `--remote` is deliberately not read. It used to select this backend
        // over a local Continuity Camera; that path is gone, so the flag is
        // accepted and ignored — a newer hoard driving an older helper, and an
        // older hoard driving this one, both still land here.
    }
}

/// runCLI is the helper's whole entry point, called by the executable target's
/// main.swift. It lives in the library rather than in top-level code so a test
/// bundle can link everything here.
///
/// Every mode either exits or blocks in `app.run()`, so this never returns
/// normally — which is also what keeps `remote` alive.
public func runCLI() {
    let args = ScanArgs(Array(CommandLine.arguments.dropFirst()))

    if args.listDevices {
        // Accessory rather than a plain process: this is a bundled app, and the
        // local-network permission macOS attributes to that bundle is the one
        // Bonjour browsing needs. Dock-less because a device query is not
        // something to take focus for.
        NSApplication.shared.setActivationPolicy(.accessory)

        // Browsing needs no pairing code — only connecting does — so this can
        // enumerate freely and let the caller sort out pairing when one is
        // chosen.
        let devices = PeerBrowser().browse(seconds: remoteBrowseWait).map {
            Device(id: $0.id, name: $0.name, kind: remoteKind)
        }
        if devices.isEmpty {
            fail(noPhoneAppMessage, code: 4)
        }
        emit(DeviceList(devices: devices))
        exit(0)
    }

    // A code is not optional. Saying so up front beats a browse that finds a
    // phone and then silently fails its handshake.
    guard let code = args.code else {
        fail("hoard-scan needs --code <six digits>, shown on the Pair tab in the app on your phone")
    }

    let app = NSApplication.shared
    // Accessory, always: a translator has no business taking focus from the
    // terminal the user is working in.
    app.setActivationPolicy(.accessory)
    let remote = RemoteController(
        deviceID: args.device, code: code,
        verifyOnly: args.verify)
    installStdinPump(
        onCommand: { remote.handle(command: $0) },
        onClosed: { remote.shutdown() })
    DispatchQueue.main.async { remote.start() }
    app.run()
}

/// installStdinPump reads the parent's verbs off stdin forever.
///
/// Commands arrive as bare lines (capture / auto-on / rearm / quit) so the
/// parent can drive the session while the terminal keeps focus. A closed stdin
/// means the parent is gone, so shut down rather than linger with an orphaned
/// link holding the phone's camera open.
func installStdinPump(onCommand: @escaping (String) -> Void,
                      onClosed: @escaping () -> Void) {
    Thread.detachNewThread {
        while let line = readLine(strippingNewline: true) {
            let cmd = line.trimmingCharacters(in: .whitespaces)
            if cmd.isEmpty { continue }
            DispatchQueue.main.async { onCommand(cmd) }
        }
        DispatchQueue.main.async { onClosed() }
    }
}

#endif
