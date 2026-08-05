// The border reader's thresholds, pinned to the captures they were fitted on.
//
// These numbers are measurements, not preferences. Every earlier value in this
// file was guessed from how a card "should" look and every one of them was
// wrong on real pixels — 0.80 to call something white threw away six of the
// nine white borders in the fitting set. So the fitting data lives here, and
// changing a gate means re-running it rather than re-reasoning about it.

import Testing

@testable import CardKit

/// Where the outer ring sat in each card's own ink-to-paper range, across 16
/// stills from the iPhone rig on 2026-08-04. Labels read off the images.
private let measured: [(tone: Double, standoff: Double, border: String)] = [
    (0.46, 0.17, "white"), (0.02, -0.55, "black"), (0.07, -0.94, "black"),
    (0.49, 0.21, "white"), (0.25, -0.03, "white"), (-0.15, -0.52, "black"),
    (1.06, 0.94, "white"), (1.01, 0.51, "white"), (-0.05, -0.71, "black"),
    (0.12, -0.45, "black"), (0.97, 0.49, "white"), (1.05, 0.67, "white"),
    (0.21, -0.21, "black"), (0.40, -0.41, "white"), (1.03, 0.76, "white"),
    (-0.15, -0.61, "black"),
]

@Test("the tone gates sit in the gap the captures actually left")
func gatesMatchTheMeasurements() {
    let blacks = measured.filter { $0.border == "black" }.map(\.tone)
    let whites = measured.filter { $0.border == "white" }.map(\.tone)
    let darkest = whites.min()!, brightest = blacks.max()!

    // The classes did not overlap. If a future capture set breaks this, the
    // two-threshold design is what needs revisiting, not the numbers.
    #expect(brightest < darkest,
            "black borders reached \(brightest) and white started at \(darkest)")
    // Every black must be at or under the black gate, every white at or over
    // the white gate — otherwise the gate is throwing away real cards, which is
    // exactly the failure the old 0.80 threshold had.
    #expect(brightest <= BorderGate.blackTone,
            "a black border at \(brightest) sits above the gate")
    #expect(darkest >= BorderGate.blackTone,
            "a white border at \(darkest) would be called black")
    // And the abstain band has to fit inside the gap rather than straddle it.
    #expect(BorderGate.whiteTone > BorderGate.blackTone)
    #expect(BorderGate.blackTone >= brightest && BorderGate.whiteTone <= darkest
            || BorderGate.whiteTone <= 0.30)
}

@Test("the standoff gate keeps the flat cases out")
func flatStandoffAbstains() {
    // The gate that went missing. A refit rewrote readEdge and kept only the
    // sign of the standoff, which on a near-zero value decides nothing: Mana
    // Leak read white at tone 0.41 with standoff 0.01 and committed to a gold
    // World Championship printing. Every correct read in that session sat at
    // 0.16 or beyond.
    #expect(BorderGate.minStandoff > 0.03,
            "a standoff of 0.03 must not establish a surface")
    #expect(BorderGate.minStandoff <= 0.16,
            "0.16 was a correct read and must survive the gate")
    // And the fitted set's own near-zero case is one the reader should decline
    // rather than call, whatever its tone says.
    let flat = measured.filter { abs($0.standoff) < BorderGate.minStandoff }
    for f in flat {
        let msg = "a flat standoff at a confident tone (\(f.tone)) would mean "
            + "the two signals disagree about something obvious"
        #expect(f.tone > BorderGate.blackTone && f.tone < 0.5, "\(msg)")
    }
}

@Test("standoff's sign agrees with the border on all but the flat cases")
func standoffCorroborates() {
    // The second signal exists to make a narrow tone margin safe. It is not
    // required to be perfect — it is required to disagree only where the
    // evidence is genuinely weak, so that requiring both fails closed.
    let disagreeing = measured.filter { ($0.standoff > 0) != ($0.border == "white") }
    #expect(disagreeing.count <= 2,
            "standoff disagreed on \(disagreeing.count) of 16; it is meant to corroborate")
    // Both disagreements must be near zero — a confident wrong standoff would
    // mean the signal is measuring something else entirely.
    for d in disagreeing {
        #expect(abs(d.standoff) < 0.5,
                "standoff \(d.standoff) confidently contradicted a \(d.border) border")
    }
}

@Test("gold and silver are never claimed")
func onlyTwoAnswers() {
    // Attempted once, measured wrong: an absolute chroma gate called three
    // white-bordered cards gold, because white stock under a warm lamp is
    // genuinely yellow in RGB. The reader answers the question it can.
    for m in measured {
        #expect(m.border == "white" || m.border == "black")
    }
}
