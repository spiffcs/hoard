import Foundation
import Testing

@testable import ScanWire

@Test("a frame survives the round trip")
func roundTrip() throws {
    let frame = Frame(kind: .ndjson, payload: Data(#"{"event":"scan"}"#.utf8))
    var reader = FrameReader()
    let out = try reader.append(encode(frame))
    #expect(out == [frame])
    #expect(reader.pending == 0)
}

@Test("every kind round-trips, including empty payloads")
func everyKind() throws {
    for kind in FrameKind.allCases {
        for payload in [Data(), Data([0x00]), Data(repeating: 0xFF, count: 5000)] {
            let frame = Frame(kind: kind, payload: payload)
            var reader = FrameReader()
            #expect(try reader.append(encode(frame)) == [frame])
        }
    }
}

@Test("a payload containing newlines is intact")
func newlinesSurvive() throws {
    let jpegish = Data([0xFF, 0xD8, 0x0A, 0x0A, 0x0D, 0x0A, 0xFF, 0xD9])
    let frame = Frame(kind: .preview, payload: jpegish)
    var reader = FrameReader()
    #expect(try reader.append(encode(frame)) == [frame])
}

@Test("three frames arriving in one read all come back")
func coalescedReads() throws {
    let frames = [
        Frame(kind: .ndjson, payload: Data("one".utf8)),
        Frame(kind: .preview, payload: Data(repeating: 7, count: 300)),
        Frame(kind: .trace, payload: Data("three".utf8)),
    ]
    var blob = Data()
    for f in frames { blob.append(try encode(f)) }
    var reader = FrameReader()
    #expect(try reader.append(blob) == frames)
}

@Test("a frame split anywhere still assembles")
func splitAnywhere() throws {
    let frame = Frame(kind: .still, payload: Data(repeating: 0xAB, count: 1000))
    let bytes = try encode(frame)
    for cut in 1..<bytes.count {
        var reader = FrameReader()
        var got = try reader.append(bytes.prefix(cut))
        #expect(got.isEmpty, "frame completed early at cut \(cut)")
        got += try reader.append(bytes.suffix(from: cut))
        #expect(got == [frame], "lost the frame at cut \(cut)")
        #expect(reader.pending == 0)
    }
}

@Test("a payload arriving one byte at a time assembles")
func dribble() throws {
    let frame = Frame(kind: .ndjson, payload: Data("dribbled".utf8))
    let bytes = try encode(frame)
    var reader = FrameReader()
    var got: [Frame] = []
    for byte in bytes { got += try reader.append(Data([byte])) }
    #expect(got == [frame])
}

@Test("an unknown kind fails loudly rather than being skipped")
func unknownKind() {
    var reader = FrameReader()
    let bogus = Data([99, 0, 0, 0, 1, 0x41])
    #expect(throws: FrameCodecError.unknownKind(99)) { _ = try reader.append(bogus) }
}

@Test("an absurd length is refused before anything is allocated for it")
func absurdLength() {
    var reader = FrameReader()
    let header = Data([FrameKind.still.rawValue, 0x7F, 0xFF, 0xFF, 0xFF])
    #expect(throws: FrameCodecError.self) { _ = try reader.append(header) }
}

@Test("encoding refuses an oversized payload rather than truncating it")
func oversizedEncode() {
    #expect(maxFramePayload == 64 * 1024 * 1024)
    let frame = Frame(kind: .still, payload: Data())
    #expect(throws: Never.self) { _ = try encode(frame) }
}

@Test("a partial frame is held, not lost")
func partialHeld() throws {
    let frame = Frame(kind: .ndjson, payload: Data("held".utf8))
    let bytes = try encode(frame)
    var reader = FrameReader()
    #expect(try reader.append(bytes.dropLast(2)).isEmpty)
    #expect(reader.pending == bytes.count - 2)
    #expect(try reader.append(bytes.suffix(2)) == [frame])
}

@Test("the json helper produces a frame the reader accepts")
func jsonHelper() throws {
    let event = Event(event: "ready", device: "test", features: ["auto"])
    let frame = try #require(Frame.json(event))
    #expect(frame.kind == .ndjson)
    var reader = FrameReader()
    let out = try reader.append(encode(frame))
    #expect(out.first?.text?.contains("\"event\":\"ready\"") == true)
}
