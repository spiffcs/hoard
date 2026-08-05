// Framing, for the link between the phone and the Mac.
//
// The protocol with the Go side is newline-delimited JSON, and that works
// because a pipe only ever carries text. The link to the phone carries preview
// JPEGs alongside those lines, and a JPEG contains newlines — so pointing NDJSON
// at a socket does not survive first contact. Base64 would survive it and cost a
// third of every frame, on the one leg where latency is the whole point.
//
// So: a five-byte header and a payload. One type byte says what the payload is,
// four say how long it is, big-endian because that is what every wire format
// does and a reader on the other end should not have to wonder.
//
// This file is pure: bytes in, frames out, no I/O and no Network.framework. That
// is deliberate — framing bugs are miserable to diagnose against a live socket
// and trivial to pin in a test, and the same codec has to run on both ends.

import Foundation

/// What a frame carries.
public enum FrameKind: UInt8, Sendable, CaseIterable {
    /// One NDJSON line — an Event from the phone, or a ScanCommand to it.
    /// Ordered, never dropped, never delayed behind a preview frame.
    case ndjson = 0
    /// A downscaled preview JPEG. Lossy and droppable by design: a stale
    /// preview frame is worse than a missing one.
    case preview = 1
    /// A full-resolution still, sent only when the Mac asks — the fixture
    /// faucet, not part of the session loop.
    case still = 2
    /// A trace line, re-emitted on the Mac's stderr so HOARD_SCAN_LOG
    /// telemetry stays whole across the network hop. Without this the phone's
    /// timing and trigger lines simply vanish, and those are the tuning loop.
    case trace = 3
}

/// One framed message.
public struct Frame: Equatable, Sendable {
    public let kind: FrameKind
    public let payload: Data

    public init(kind: FrameKind, payload: Data) {
        self.kind = kind
        self.payload = payload
    }
}

/// The header is one type byte plus a four-byte big-endian length.
public let frameHeaderSize = 5

/// A payload ceiling, so a corrupt or hostile length cannot make the reader
/// allocate arbitrarily while it waits for bytes that will never arrive. A
/// 48 MP still is comfortably inside it; nothing legitimate is not.
public let maxFramePayload = 64 * 1024 * 1024

public enum FrameCodecError: Error, Equatable {
    case unknownKind(UInt8)
    case payloadTooLarge(Int)
}

/// encode turns a frame into bytes.
public func encode(_ frame: Frame) throws -> Data {
    guard frame.payload.count <= maxFramePayload else {
        throw FrameCodecError.payloadTooLarge(frame.payload.count)
    }
    var out = Data(capacity: frameHeaderSize + frame.payload.count)
    out.append(frame.kind.rawValue)
    let n = UInt32(frame.payload.count)
    out.append(UInt8(truncatingIfNeeded: n >> 24))
    out.append(UInt8(truncatingIfNeeded: n >> 16))
    out.append(UInt8(truncatingIfNeeded: n >> 8))
    out.append(UInt8(truncatingIfNeeded: n))
    out.append(frame.payload)
    return out
}

/// FrameReader accumulates bytes and hands back whole frames.
///
/// A stream reader rather than a decode function because TCP has no opinion
/// about message boundaries: a read can deliver half a header, three frames, or
/// a header now and its payload in four pieces over the next second. Every one
/// of those has to work, and code that assumes otherwise fails only under load —
/// which is exactly when a scanning session is doing something interesting.
public struct FrameReader {
    private var buffer = Data()

    public init() {}

    /// How many bytes are held pending a complete frame. Exposed for tests and
    /// for a health check: a reader that only grows is a reader whose peer is
    /// sending something it does not understand.
    public var pending: Int { buffer.count }

    /// append adds bytes and returns every complete frame they finished.
    ///
    /// Throws on a kind it does not recognise rather than skipping it. A newer
    /// peer sending a frame type this build has never heard of means the two
    /// ends disagree about the protocol, and continuing past that reads the
    /// next header out of the middle of a payload — the stream is already lost,
    /// and failing loudly is the only honest option.
    public mutating func append(_ bytes: Data) throws -> [Frame] {
        buffer.append(bytes)
        var frames: [Frame] = []
        while true {
            guard buffer.count >= frameHeaderSize else { break }
            let header = [UInt8](buffer.prefix(frameHeaderSize))
            guard let kind = FrameKind(rawValue: header[0]) else {
                throw FrameCodecError.unknownKind(header[0])
            }
            let length =
                Int(header[1]) << 24 | Int(header[2]) << 16
                | Int(header[3]) << 8 | Int(header[4])
            guard length <= maxFramePayload else {
                throw FrameCodecError.payloadTooLarge(length)
            }
            let total = frameHeaderSize + length
            guard buffer.count >= total else { break }
            let start = buffer.index(buffer.startIndex, offsetBy: frameHeaderSize)
            let end = buffer.index(buffer.startIndex, offsetBy: total)
            frames.append(Frame(kind: kind, payload: Data(buffer[start..<end])))
            buffer.removeSubrange(buffer.startIndex..<end)
        }
        return frames
    }
}

// MARK: - Convenience

extension Frame {
    /// A frame carrying one encodable message as an NDJSON line.
    public static func json(_ value: some Encodable) -> Frame? {
        guard let data = try? JSONEncoder().encode(value) else { return nil }
        return Frame(kind: .ndjson, payload: data)
    }

    /// The payload decoded as UTF-8, for the line-shaped kinds.
    public var text: String? { String(data: payload, encoding: .utf8) }
}
