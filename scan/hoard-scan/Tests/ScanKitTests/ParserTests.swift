// The text heuristics: which line is a title, which is furniture, and what a
// collector block means.
//
// Every case here is a real card that defeated the obvious rule. The fixture
// sweep pins these too, but only end to end and only on the 26 frames that
// happen to contain them — a predicate is far easier to reason about when you
// can ask it one question at a time, which is the point of this file.

import BorderKit
import Testing

@testable import ScanKit

// MARK: - Title selection

@Test("a title needs two words and mostly capitals")
func titlesLookLikeTitles() {
    #expect(titleLike("Sacred Ground"))
    #expect(titleLike("Erebos. God of the Dead"))
    #expect(titleLike("companion Animals") == false)
}

@Test("the border block reads as all caps and is never a title")
func allCapsFurnitureIsNotATitle() {
    // Left alone, an artist credit fuzzy-resolves to a real card and ghosts
    // into the queue: Kev Walker the artist became Kiln Walker the card. A
    // multi-word line with at most one lowercase letter is frame furniture.
    #expect(titleLike("KEy WALKER") == false)
    #expect(titleLike("MSH EN ADI GRANOY") == false)

    // The rule carries a six-letter floor, so short fragments do not reach it.
    // titleLike's own comment lists "IN & C" among the lines it rejects, and
    // that is not what the code does — three letters is under the floor, and
    // no other predicate catches it either. Pinned as-is rather than
    // "corrected": widening the floor is a tuning change that has to be argued
    // against scan/corpus and a live session, not slipped in beside a refactor.
    #expect(titleLike("IN & C"))
}

@Test("rules text that opens with its trigger word is not a title")
func triggerWordsAreNotTitles() {
    // These capitalize exactly like a title. No card is named "When…".
    #expect(titleLike("Whenever Black Panther") == false)
    #expect(titleLike("When Parallel Thoughts comes into") == false)
}

@Test("a flavor attribution's dash is never a title")
func flavorAttributionIsNotATitle() {
    // The "—Doctor Doom" phantom. The first-letter guard happens to reject
    // these too; the explicit dash check is what survives loosening it.
    #expect(titleLike("—Doctor Doom") == false)
}

@Test("a type line is not a title")
func typeLinesAreNotTitles() {
    #expect(titleLike("Legendary Creature") == false)
    #expect(titleLike("Artifact Creature") == false)
}

@Test("a card naming itself in its rules text is caught, and the name mined back")
func selfReferenceIsRecovered() {
    // The title band is lost and the card names itself. "Dwarven Ruins" has to
    // be recoverable as a candidate, and the line must not be taken wholesale.
    let line = "Dwarven Ruins comes into play tapped."
    #expect(titleLike(line) == false)
    #expect(parseSelfReference(line) == "Dwarven Ruins")
}

// MARK: - Collector numbers

@Test("a creature's power and toughness is not a collector number")
func powerToughnessIsNotACollectorNumber() {
    // "2/2" matches the pair regex perfectly. The guard is that a pair only
    // counts when the total is >= 20 or the numerator is zero-padded.
    #expect(parseCollectorInfo(["2/2"]).isEmpty)
    #expect(parseCollectorInfo(["4/5"]).isEmpty)
}

@Test("a real pair-form number is read, and is its own corroboration")
func pairFormNumbersAreRead() {
    // A pair with a plausible total does not share its shape with a mana cost
    // or a power box, so it survives even when no set code does.
    let reads = parseCollectorInfo(["29/143"])
    #expect(reads.first?.number == "29")
    #expect(reads.first?.pair == true)
}

@Test("zero padding is dropped from the printed number")
func zeroPaddingIsNormalized() {
    #expect(normalizeNumber("0123") == "123")
    #expect(normalizeNumber("0000") == "0")
}

@Test("prose must not donate a set code")
func proseIsNotASetCode() {
    // "…and put it into your hand" parsed as set PUT plus Italian, and
    // "…and it ain't you!" parsed as set AND. Gating extraction on the line
    // reading like border print is what stopped both.
    #expect(setLangFurniture("and put it into your hand") == false)
    #expect(setLangFurniture("Whenever a spell or ability an") == false)
}

// MARK: - Text folding

@Test("lookalike glyphs fold to ASCII before anything is matched")
func confusablesAreFolded() {
    // Marvel frames print the rarity before the number ("R 0657"), and
    // mythic's M arrives as Cyrillic М until asciify folds it. Never assume
    // the M15 layout is the only layout.
    #expect(asciify("\u{041C}15") == "M15")  // Cyrillic М
    #expect(asciify("m15") == "M15")
}

@Test("a bare four-digit number in the copyright range is a year, not a number")
func yearsAreNotCollectorNumbers() {
    #expect(looksLikeAYear("1996"))
    #expect(looksLikeAYear("2024"))
    #expect(looksLikeAYear("350") == false)
}

@Test("titles are compared loosely enough to survive one OCR slip")
func nearlyEqualTitlesAreTheSameCard() {
    // The helper must not invent: a misread ships as read and downstream fuzzy
    // matching owns the fix. But the merge still has to recognize two readings
    // of one card — "Etemal Dragon" and "Eternal Dragon" are the same capture.
    #expect(sameTitle("Eternal Dragon", "Etemal Dragon"))
    #expect(sameTitle("Eternal Dragon", "Green Dragon") == false)
    #expect(editDistance("Tremor", "Tremor") == 0)
    #expect(editDistance("Tremor", "Tremer") == 1)
}
