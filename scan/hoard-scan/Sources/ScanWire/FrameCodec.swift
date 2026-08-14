import Foundation

public enum FrameKind: UInt8, Sendable, CaseIterable {
    case ndjson = 0
    case preview = 1
    case still = 2
    case trace = 3
}

public struct Frame: Equatable, Sendable {
    public let kind: FrameKind
    public let payload: Data

    public init(kind: FrameKind, payload: Data) {
        self.kind = kind
        self.payload = payload
    }
}

public let frameHeaderSize = 5

public let maxFramePayload = 64 * 1024 * 1024

public enum FrameCodecError: Error, Equatable {
    case unknownKind(UInt8)
    case payloadTooLarge(Int)
}

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

public struct FrameReader {
    private var buffer = Data()

    public var limit = maxFramePayload

    public init() {}

    public init(limit: Int) {
        self.limit = limit
    }

    public var pending: Int { buffer.count }

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
            guard length <= limit else {
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

extension Frame {
    public static func json(_ value: some Encodable) -> Frame? {
        guard let data = try? JSONEncoder().encode(value) else { return nil }
        return Frame(kind: .ndjson, payload: data)
    }

    public var text: String? { String(data: payload, encoding: .utf8) }
}
