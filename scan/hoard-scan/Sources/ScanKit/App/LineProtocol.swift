// Keeping one event on one line.

// macOS only. ScanKit is the Mac end of the link. See Package.swift.
#if os(macOS)

import Foundation

/// ndjsonLine flattens a payload so it can be written as a single line.
///
/// The phone's frames are passed through undecoded — the wire contract promises
/// forward compatibility, and a round trip through a struct on this side would
/// silently drop any field this build happens not to know about. But "verbatim"
/// and "one line" are in tension: the frame codec explicitly permits newlines in
/// a payload, while stdout here is the Go side's *line* parser. A raw \n or \r
/// inside a payload splits one event into two half-lines and poisons everything
/// after it in the stream.
///
/// So interior newline bytes become spaces. A legal JSON encoder never emits
/// them — it writes the two-byte escape \n instead — which is what makes this
/// safe rather than lossy: the only payload it rewrites was already going to
/// break the pipe.
///
/// Byte-wise on purpose, and safe for UTF-8: every byte of a multi-byte UTF-8
/// sequence has its high bit set, so 0x0A and 0x0D can only ever appear as
/// themselves and never as part of another character.
func ndjsonLine(_ payload: Data) -> Data {
    var line = payload
    for i in line.indices where line[i] == 0x0A || line[i] == 0x0D {
        line[i] = 0x20
    }
    line.append(0x0A)
    return line
}

#endif
