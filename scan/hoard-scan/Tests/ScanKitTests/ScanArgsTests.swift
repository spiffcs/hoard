// The command line the Go side actually sends.
//
// internal/scan builds these argument lists, and a parse that quietly drops a
// flag shows up as a scanning session that ignores --device or a pairing check
// that never runs. The edge cases below are all reachable from a typo in a
// hoard flag, not hypotheticals.

// macOS only, like everything in ScanKit. See Package.swift.
#if os(macOS)

import Testing

@testable import ScanKit

@Test("a full session line parses")
func scanArgsFullLine() {
    let a = ScanArgs(["--remote", "--code", "123456", "--device", "iPhone-7F2A"])
    #expect(a.code?.digits == "123456")
    #expect(a.device == "iPhone-7F2A")
    #expect(!a.verify)
    #expect(!a.listDevices)
}

@Test("--list-devices is recognised on its own")
func scanArgsListDevices() {
    let a = ScanArgs(["--list-devices"])
    #expect(a.listDevices)
    #expect(a.code == nil)
}

@Test("--verify is recognised alongside a code")
func scanArgsVerify() {
    let a = ScanArgs(["--code", "000000", "--verify"])
    #expect(a.verify)
    #expect(a.code?.digits == "000000")
}

@Test("a missing code parses to nil rather than to something wrong")
func scanArgsNoCode() {
    #expect(ScanArgs([]).code == nil)
    #expect(ScanArgs(["--device", "iPhone-7F2A"]).code == nil)
}

@Test("a trailing --code with no value does not read past the end")
func scanArgsTrailingCode() {
    // The `i + 1 < args.count` guard. Without it this is an out-of-range crash
    // on a typo, which is a helper that dies before it can say what was wrong.
    #expect(ScanArgs(["--code"]).code == nil)
    #expect(ScanArgs(["--device", "iPhone-7F2A", "--code"]).code == nil)
}

@Test("a trailing --device with no value does not read past the end")
func scanArgsTrailingDevice() {
    #expect(ScanArgs(["--code", "123456", "--device"]).device == nil)
    #expect(ScanArgs(["--device"]).device == nil)
}

@Test("a code that is not six digits is rejected")
func scanArgsBadCode() {
    // PairingCode strips non-digits and then demands exactly six, so all three
    // of these fail — and failing here is what produces the "needs --code <six
    // digits>" message instead of a browse that finds a phone and then dies in
    // the handshake.
    #expect(ScanArgs(["--code", "12345"]).code == nil)
    #expect(ScanArgs(["--code", "1234567"]).code == nil)
    #expect(ScanArgs(["--code", "abcdef"]).code == nil)
}

@Test("a grouped code is accepted the way it is read aloud")
func scanArgsGroupedCode() {
    // PairingCode.display prints "123 456", so a user retyping what the phone
    // shows types a space. Non-digits are stripped before the length check,
    // which means that paste works.
    #expect(ScanArgs(["--code", "123 456"]).code?.digits == "123456")
    #expect(ScanArgs(["--code", "123-456"]).code?.digits == "123456")
}

@Test("--remote is accepted and ignored")
func scanArgsRemoteIgnored() {
    // The flag used to select this backend over a local Continuity Camera.
    // That path is gone, but the Go side still sends the flag, so it must
    // parse as a no-op rather than as an error or as a device name.
    let a = ScanArgs(["--remote", "--code", "123456"])
    #expect(a.code?.digits == "123456")
    #expect(a.device == nil)
}

@Test("an unknown flag is ignored rather than fatal")
func scanArgsUnknownFlag() {
    // Forward compatibility runs both directions: a newer hoard may pass a
    // flag this helper has never heard of, and the session should still start.
    let a = ScanArgs(["--code", "123456", "--future-flag", "value"])
    #expect(a.code?.digits == "123456")
}

#endif
