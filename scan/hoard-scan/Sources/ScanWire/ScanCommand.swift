// The verbs the parent sends on stdin.

import Foundation

/// ScanCommand is one line of the parent's half of the conversation.
///
/// Parsing is separated from performing because the two have very different
/// stakes. Performing needs a camera and a window; parsing is a contract with
/// the Go side (internal/scan/session_darwin.go) that a test can check in
/// microseconds — and a mistyped verb here is a feature that silently does
/// nothing on a helper the parent believes supports it.
///
/// An unknown verb is deliberately not an error case here: the caller reports
/// it and keeps the session alive, because a newer hoard talking to an older
/// helper is a supported pairing.
/// Three verbs are gone: `rotate-left`/`rotate-right`, `frame-on`/`frame-off`
/// and `effects`. All three were Continuity Camera concepts — a preview the Mac
/// had to turn upright, Center Stage, and the system Video Effects panel — and
/// the phone answered none of them. They are not reserved: an old hoard sending
/// one gets the same "unknown verb" treatment as a typo, which is exactly the
/// forward/backward compatibility this type already promises.
public enum ScanCommand: Equatable {
    case capture
    case torch(Bool)
    case auto(Bool)
    case rearm
    /// Ask the phone to send each capture's full-resolution still back, so a
    /// session builds a fixture set. Off unless the parent asks: a 4032x3024
    /// JPEG per card is far more than the wire carries in normal use, and the
    /// whole point of reading on the phone is not shipping these.
    case stills(Bool)
    /// Set the trigger's stillness knobs: consecutive still samples, and the
    /// seconds between samples. Their product is the floor on how long a card
    /// must sit before the shutter fires, which is the largest single term in
    /// the wait from "holding still" to the confirming sound.
    ///
    /// Sent rather than baked so a session can A/B them. The ledger's warning
    /// against shortening the streak was fitted on the Continuity rig, which
    /// had neither a motion sensor nor locked metering; whether it holds on a
    /// phone is an empirical question and this is how it gets asked.
    case tune(stable: Int, interval: Double)
    case chime
    /// The `result` verb is the only one carrying a payload — scan.HUDResult as
    /// compact JSON, decoded as HUDCommand.
    case result(payload: String)
    case quit

    /// init parses one stdin line: a verb plus an optional payload after the
    /// first space. Returns nil for anything unrecognized.
    public init?(line: String) {
        let parts = line.split(separator: " ", maxSplits: 1)
        let payload = parts.count > 1 ? String(parts[1]) : ""
        switch parts.first.map(String.init) ?? "" {
        case "capture": self = .capture
        case "torch-on": self = .torch(true)
        case "torch-off": self = .torch(false)
        case "auto-on": self = .auto(true)
        case "auto-off": self = .auto(false)
        case "rearm": self = .rearm
        case _ where line.hasPrefix("tune "):
            // "tune <stable> <interval>", both required — a half-specified
            // tuning is a worse experiment than none.
            let f = line.split(separator: " ")
            guard f.count == 3, let n = Int(f[1]), let i = Double(f[2]),
                  n > 0, i > 0 else { return nil }
            self = .tune(stable: n, interval: i)
        case "stills-on": self = .stills(true)
        case "stills-off": self = .stills(false)
        case "chime": self = .chime
        case "result": self = .result(payload: payload)
        case "quit": self = .quit
        default: return nil
        }
    }
}
