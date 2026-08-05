// What the terminal is told when the link drops.
//
// Pinned because the failure being guarded against is not a crash — it is a
// sentence. A live session showed the user "The operation couldn't be
// completed. (Network.NWError error 53 - Software caused connection abort)",
// which names an errno, blames software in general, and mentions neither the
// iPhone nor the one action that fixes it.

import Network
import Testing

@testable import ScanLink

@Test("a suspended app reads as something the user can act on")
func backgroundedAppMessage() {
    let f = LinkFailure(NWError.posix(.ECONNABORTED))
    #expect(f.reason.contains("Hoard Scan"), "should name the app: \(f.reason)")
    #expect(!f.reason.contains("NWError"))
    #expect(!f.reason.contains("errno"))
    // The framework's words survive for the log, just not for the terminal.
    #expect(!f.detail.isEmpty)
}

@Test("no user-facing reason leaks framework vocabulary")
func noInternalsInAnyReason() {
    let errors: [NWError] = [
        .posix(.ECONNABORTED), .posix(.ECONNRESET), .posix(.ETIMEDOUT),
        .posix(.ENETDOWN), .posix(.EHOSTUNREACH), .posix(.ECONNREFUSED),
        .posix(.ENOTCONN), .posix(.EPIPE), .posix(.EINVAL), .posix(.EFAULT),
    ]
    for e in errors {
        let r = LinkFailure(e).reason
        for leak in ["NWError", "POSIX", "posix", "error 5", "operation couldn't",
                     "Software caused", "errno", "Network."] {
            #expect(!r.contains(leak), "\(r) leaks \(leak)")
        }
        #expect(!r.isEmpty)
        // User-facing sentences start with a capital. "iPhone" is the exception
        // that proves it is a sentence and not a framework string: it is
        // capitalised the way Apple writes it, not the way a noun would be.
        #expect(r.first?.isUppercase == true || r.hasPrefix("iPhone"),
                "reason does not start like a sentence: \(r)")
    }
}

@Test("an unrecognised error still says something true and plain")
func unknownErrorFallsBack() {
    let f = LinkFailure(NWError.posix(.EILSEQ))
    #expect(f.reason == "iPhone disconnected")
    #expect(!f.detail.isEmpty)
}
