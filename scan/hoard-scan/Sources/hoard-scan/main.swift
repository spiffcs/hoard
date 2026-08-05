// The executable target: a shell around ScanKit.
//
// Swift requires top-level code to live in a file named main.swift, and a file
// with top-level code cannot be linked into a test bundle. Keeping this to one
// call is what lets Tests/ScanKitTests reach the whole helper with @testable.

import ScanKit

runCLI()
