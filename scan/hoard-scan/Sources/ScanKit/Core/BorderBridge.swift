// The Mac's call into the shared border reader.
//
// A thin adapter and deliberately nothing more. The reader moved to BorderKit
// so the phone could use it too, and it takes the three things it actually
// reads rather than a whole CardRead — which is what let it move without
// dragging this pipeline's types along. This keeps the old call shape so
// ScanFrame and the CLI read as they did.

import BorderKit
import CoreGraphics

func readBorder(_ cg: CGImage, _ read: CardRead) -> BorderReading {
    readBorder(cg, lines: read.lines, bandLines: read.bandLines,
               copyrightYear: read.copyrightYear)
}

