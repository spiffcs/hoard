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
enum ScanCommand: Equatable {
    case capture
    case rotate(clockwise: Bool)
    case framing(Bool)
    case torch(Bool)
    /// The system Video Effects panel: Studio Light (the only software lighting
    /// macOS offers, since the torch isn't bridged), plus the system's own
    /// Center Stage and Desk View toggles.
    case effects
    case auto(Bool)
    case rearm
    case chime
    /// The `result` verb is the only one carrying a payload — scan.HUDResult as
    /// compact JSON, decoded as HUDCommand.
    case result(payload: String)
    case quit

    /// init parses one stdin line: a verb plus an optional payload after the
    /// first space. Returns nil for anything unrecognized.
    init?(line: String) {
        let parts = line.split(separator: " ", maxSplits: 1)
        let payload = parts.count > 1 ? String(parts[1]) : ""
        switch parts.first.map(String.init) ?? "" {
        case "capture": self = .capture
        case "rotate-left": self = .rotate(clockwise: false)
        case "rotate-right": self = .rotate(clockwise: true)
        case "frame-on": self = .framing(true)
        case "frame-off": self = .framing(false)
        case "torch-on": self = .torch(true)
        case "torch-off": self = .torch(false)
        case "effects": self = .effects
        case "auto-on": self = .auto(true)
        case "auto-off": self = .auto(false)
        case "rearm": self = .rearm
        case "chime": self = .chime
        case "result": self = .result(payload: payload)
        case "quit": self = .quit
        default: return nil
        }
    }
}
