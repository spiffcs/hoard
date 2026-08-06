// Which line of a card is its title, and which lines are furniture wearing a
// title's shape. Every predicate here exists because a real card defeated the
// obvious rule; see docs/scanner-tuning.md.

import BorderKit
import Foundation

/// typeLineWords are tokens that mark a card's *type* line ("Legendary
/// Enchantment Creature — God"), which reads at title-like isolation and
/// title-like capitalization. Card names essentially never use these words,
/// so a token match is a safe rejection.
let typeLineWords: Set<String> = [
    "legendary", "creature", "enchantment", "planeswalker",
    "sorcery", "instant", "battle", "tribal", "snow",
]

/// titleLike judges whether a frame line could be a card's title band, by the
/// text alone. Geometry cannot do this job: in a booster-sized cascade the
/// stacked title bands sit closer together than a rules paragraph's spacing,
/// so any gap threshold either eats real titles or admits rules text. Shape
/// separates them instead — Magic titles are Title Case multi-word lines,
/// rules text is sentence case, type lines carry known type words. Filtered
/// generously: a survivor that isn't a card dies on the Go side's Scryfall
/// fuzzy match. Single-word names (Ponder, Opt) are rejected here but not
/// lost — a lone card always has an outline, so the crop channel carries it.
func titleLike(_ s: String) -> Bool {
    if boilerplate(s) { return false }
    let words = s.split(whereSeparator: { $0.isWhitespace })
    guard words.count >= 2 else { return false }
    // A leading dash is a flavor attribution ("—Doctor Doom"), never a
    // title. The first-letter guard below happens to reject these today;
    // explicit so the intent survives any loosening of that guard.
    if let first = s.first, "—–-―‒−".contains(first) { return false }
    guard let first = words.first?.first, first.isLetter else { return false }
    let tokens = words.map { String($0.lowercased().filter { $0.isLetter }) }
    if tokens.contains(where: { typeLineWords.contains($0) }) { return false }
    // Rules text that opens a line with its trigger word capitalizes like a
    // title ("Whenever Black Panther…", "When Parallel Thoughts comes into").
    // No card is named "When…", so the lead token alone is a safe rejection.
    if let lead = tokens.first, lead == "whenever" || lead == "when" { return false }
    // A card's rules text names the card itself, so the self-reference reads
    // as Title Case for exactly as long as the name runs and then trails off
    // into a sentence ("Dwarven Ruins comes into play tapped."). The idiom
    // that follows the name is the tell — and it is worth catching, because
    // these lines otherwise pass every test below and become the card's name.
    // parseSelfReference mines the name back out of them.
    if selfReferenceIdiom(tokens) != nil { return false }
    // The border block prints in small caps and reads as (nearly) all caps —
    // "KEy WALKER", "IN & C", a mangled set line — while real card titles are
    // Title Case with plenty of lowercase. A multi-word line with at most one
    // lowercase letter is frame furniture; left alone, an artist credit fuzzy-
    // resolves to a real card and ghosts into the queue (observed live: Kev
    // Walker the artist became Kiln Walker the card).
    let letters = s.filter { $0.isLetter }
    if letters.count >= 6 && letters.filter({ $0.isLowercase }).count <= 1 { return false }
    var caps = 0
    for w in words where w.first?.isUppercase == true { caps += 1 }
    // Titles capitalize everything but connectors; sentences capitalize
    // little. Strictly more than half keeps "Erebos. God of the Dead" and
    // rejects "companion Animals".
    return caps * 2 > words.count
}

/// flavorAttribution reports whether a line hangs directly beneath a flavor
/// quote — the "—Doctor Doom" under "Beneath me." An attribution names a
/// character, and in licensed sets the character is usually a card in the
/// same set, so the Scryfall backstop that kills other junk *vouches* for
/// this phantom instead (observed live: Aerial Doombot's flavor text queued
/// a Doctor Doom). OCR routinely drops the attribution dash, so the quote
/// above is the reliable signal. On a tilted card the axis-aligned boxes of
/// adjacent lines bleed into each other — the fixture's quote box vertically
/// *contains* its attribution — so the relation is "centered inside or just
/// below the quote's vertical span", not a clean gap between boxes. A
/// neighbouring card's real title band sits past an attribution line, a
/// bottom margin, and a border — well below the reach of the allowance.
func flavorAttribution(_ line: Line, among all: [Line]) -> Bool {
    let cy = line.box.midY
    for other in all {
        if other.box == line.box { continue } // a stray quote glyph on a title must not self-match
        guard endsQuoted(other.text) else { continue }
        // Vision origin is bottom-left: lower on the card is smaller Y.
        guard cy < other.box.maxY, cy > other.box.minY - 1.5 * line.box.height else { continue }
        if min(other.box.maxX, line.box.maxX) > max(other.box.minX, line.box.minX) {
            return true
        }
    }
    return false
}

/// endsQuoted matches a flavor quote's closing mark, whatever glyph Vision
/// chose for it.
func endsQuoted(_ s: String) -> Bool {
    guard let last = s.trimmingCharacters(in: .whitespaces).last else { return false }
    return "\"\u{201D}\u{00BB}'\u{2019}".contains(last)
}

/// sameTitle reports whether two reads plausibly name the same card. The
/// tolerance exists because the frame pass and a perspective-corrected crop
/// routinely OCR the same printed title differently ("Ulamoz, tre" vs
/// "Ulamos, the") — and a missed match here becomes the same card twice in
/// the confirm queue, which a user will confirm twice and double-count.
func sameTitle(_ a: String, _ b: String) -> Bool {
    let x = normTitle(a), y = normTitle(b)
    if x.isEmpty || y.isEmpty { return false }
    if x == y { return true }
    if (x.count >= 8 && y.contains(x)) || (y.count >= 8 && x.contains(y)) { return true }
    return editDistance(x, y) * 4 <= max(x.count, y.count) // ≤ a quarter differs
}

/// selfReferenceIdiom finds where a line stops naming a card and starts
/// describing it, returning the index of the first idiom token. The runs are
/// deliberately short and must sit past the first token — there has to be a
/// name in front of them for the phrase to be self-reference at all.
func selfReferenceIdiom(_ tokens: [String]) -> Int? {
    let idioms = [["comes", "into"], ["enters", "the"], ["leaves", "play"]]
    guard tokens.count >= 2 else { return nil }
    for start in 1..<tokens.count {
        for idiom in idioms where start + idiom.count <= tokens.count {
            if Array(tokens[start..<(start + idiom.count)]) == idiom { return start }
        }
    }
    return nil
}

/// parseSelfReference recovers a card's name from its own rules text. Magic
/// cards name themselves, so an old frame whose title band was lost — the
/// common failure, where the serif title sits against the art and the band
/// crop returns fragments — is usually still named in plain text further down
/// ("Dwarven Ruins comes into play tapped."). Two cards were total losses in
/// one live session with their names sitting on the wire this way.
///
/// The result ships as an extra candidate only, never as the entry's name: it
/// is a guess built from a heuristic, and the resolver already owns choosing
/// among candidates.
func parseSelfReference(_ s: String) -> String? {
    var words = s.split(whereSeparator: { $0.isWhitespace }).map(String.init)
    if let lead = words.first?.lowercased().filter({ $0.isLetter }),
        lead == "when" || lead == "whenever" {
        words.removeFirst()
    }
    let tokens = words.map { String($0.lowercased().filter { $0.isLetter }) }
    guard let idx = selfReferenceIdiom(tokens) else { return nil }
    let lead = Array(words[0..<idx])
    // A name is Title Case throughout; a lowercase word means the run started
    // mid-sentence and the "name" would be a fragment of prose.
    guard lead.allSatisfy({ $0.first?.isUppercase == true }) else { return nil }
    let name = lead.joined(separator: " ")
        .trimmingCharacters(in: CharacterSet(charactersIn: " ,.:;"))
    // One word left over is as often a pronoun ("It comes into play") as a
    // name, and single-word names already have the crop channel.
    guard name.split(whereSeparator: { $0.isWhitespace }).count >= 2 else { return nil }
    return name
}

/// addCandidate records an alternate reading of an entry's title. The merge
/// ladder has to pick one name per card, but the reading it drops is often the
/// one downstream fuzzy matching could have used — so the loser rides along
/// instead of being discarded. Nothing empty, nothing already present, and the
/// same prefix cap the crop channel applies.
/// Dedup is exact-normalized, deliberately not sameTitle: its containment
/// tolerance would treat "Shivan Oasis" as already present in the rules line
/// "Shivan Oasis comes into play tapped." and drop the very reading worth
/// keeping.
func addCandidate(_ entry: inout CardEntry, _ name: String) {
    let t = name.trimmingCharacters(in: .whitespacesAndNewlines)
    guard !t.isEmpty, entry.candidates.count < 8 else { return }
    let key = normTitle(t)
    guard !key.isEmpty, !entry.candidates.contains(where: { normTitle($0) == key }) else { return }
    entry.candidates.append(t)
}



/// boilerplate matches the card frame's own print that reads at title-like
/// isolation and capitalization — the copyright border line, the artist
/// credit, and the collector block — which would otherwise become phantom
/// queue entries on every capture that shows a card's bottom.
func boilerplate(_ s: String) -> Bool {
    let t = s.lowercased()
    if t.contains("wizards of the coast") || t.hasPrefix("illus")
        || s.hasPrefix("™") || s.hasPrefix("©")
        || s.contains("•") { // the collector line's separator; never in a name
        return true
    }
    // The set/language line survives the bullet check whenever Vision reads
    // the bullet as "*" or a bare space ("MSH *EN ADI GRANOY"). If the line
    // parses as a set code beside a language code, it is the border, whatever
    // the separator became.
    if group(setLangRE, asciify(s)) != nil {
        return true
    }
    // Licensed frames add their own brand line, and "© MARVEL" reads as
    // "C MARVEL" or "O MARVEL." at this glyph size: a lone character in front
    // of a brand word is a mangled © symbol, not a card name.
    let words = t.split(whereSeparator: { $0.isWhitespace })
        .map { $0.trimmingCharacters(in: .punctuationCharacters) }
    if words.count == 2, words[0].count == 1, words[1] == "marvel" {
        return true
    }
    if artistCredit(s) {
        return true
    }
    // Old-frame copyright fragments ("Coast, Ine: 30/1", "te Coast, Inc")
    // survive every check above — no bullet, no full "wizards of the coast",
    // Title Case enough — and became phantom entries (observed live).
    return copyrightFurniture(s)
}
