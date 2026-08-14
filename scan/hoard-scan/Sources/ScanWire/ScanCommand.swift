import Foundation

public enum ScanCommand: Equatable {
    case capture
    case torch(Bool)
    case auto(Bool)
    case rearm
    case stills(Bool)
    case tune(stable: Int, interval: Double)
    case evBias(Double)
    case chime
    case result(payload: String)
    case quit

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
            let f = line.split(separator: " ")
            guard f.count == 3, let n = Int(f[1]), let i = Double(f[2]),
                  n > 0, i > 0 else { return nil }
            self = .tune(stable: min(max(n, 1), 60),
                         interval: min(max(i, 0.01), 5.0))
        case _ where line.hasPrefix("evbias "):
            guard let v = Double(payload), v >= -8, v <= 8 else { return nil }
            self = .evBias(v)
        case "stills-on": self = .stills(true)
        case "stills-off": self = .stills(false)
        case "chime": self = .chime
        case "result": self = .result(payload: payload)
        case "quit": self = .quit
        default: return nil
        }
    }
}
