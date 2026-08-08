// The executable target: a shell around ScanKit.
//
// Swift requires top-level code to live in a file named main.swift, and a file
// with top-level code cannot be linked into a test bundle. Keeping this to one
// call is what would let a ScanKitTests target reach the whole helper with
// @testable — no such target exists yet, so ScanKit's coverage today is the
// downstream fixture and corpus harnesses, not unit tests.

import ScanKit

// runCLI() is the macOS helper's entry point and lives behind the same fence
// App/ does. An iOS build of this target therefore links an empty main, which
// is harmless: the phone runs the ScanKit library from its own app target, not
// this executable.
#if os(macOS)
    runCLI()
#endif
