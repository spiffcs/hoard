// The price tiers' voices, on the phone.
//
// Every voice belongs to exactly one tier. That is not a UI rule enforced
// somewhere else — it is a field on the voice itself, and the Settings picker
// is derived from it, so Bulk can only ever offer Bulk's voices and a harp run
// can never land on a ten-cent common. The tiers are told apart by weight:
// bulk is short and dry, the win tiers ring, the jackpot runs, and review asks
// a question. A voice that could appear anywhere would undo that.
//
// Deriving the palettes from this one table also makes the dangerous mistake
// unrepresentable. A palette that named a voice with no spec would leave its
// tier silently mute — play() finds no buffer and simply returns — and nothing
// in the app would report it. There is no second list to fall out of step.
//
// Silence is the one voice with no spec, and it is safe precisely because it is
// not an accident of a missing buffer: `silence` is offered by every tier, and
// play() refuses it by name before it ever reaches the buffer lookup. A tier
// that says nothing because it was asked to and a tier that says nothing
// because its voice went missing are two different events, and this file is
// where they are told apart.
//
// The four original voices keep their exact synthesis parameters (frequencies,
// durations, decay, soft clip) because people have already learned them by ear.
// They were once shared with a macOS helper's SoundBank; that helper is gone
// and this is now the only audio in the project, so the parameters are free to
// change — they are held for the people using them, not for a counterpart.
//
// Everything is synthesized into PCM buffers once at init — additive sine bursts
// with exponential decay — so the app bundles no audio files and owes no one a
// license. Strike frequencies are fixed for the strike's life, so there are no
// glides: every voice is built from discrete hits.
//
// The rules that matter more than the waveforms:
//   · exactly one sound per card, played when the scan *resolves*, never at the
//     shutter — a shutter pop on top made every card a two-beep event
//   · a new sound cuts the old one; tails never stack or queue, because a rapid
//     next card should clip the last fanfare rather than wait behind it
//   · a running-total update is always silent

import AVFoundation
import Foundation

/// One strike: when it starts, its partials, how long it rings, how loud.
private struct Strike {
    let at: Double
    let freqs: [Double]
    let dur: Double
    let amp: Double
}

/// One assignable voice, as the Settings picker sees it.
///
/// `tier` is what scopes the picker. It is stored on the voice rather than in a
/// separate per-tier list so the two can never disagree.
struct SoundVoice: Identifiable, Equatable {
    let id: String
    let label: String
    let tier: Tier
}

/// A voice's definition: what the picker shows, and what the renderer plays.
private struct VoiceSpec {
    let id: String
    let label: String
    let tier: Tier
    let strikes: [Strike]
    /// Buffer length. Must cover the last strike's `at + dur` or the tail is
    /// clipped — render() truncates silently rather than complaining.
    let seconds: Double
}

/// The voices, in picker order: tier by tier, each tier's default first.
private let voiceTable: [VoiceSpec] = [

    // MARK: Bulk — short, dry, low. A tick that means "next", not "look".

    /// A low woody knock, gone in 50 ms.
    VoiceSpec(id: "knock", label: "Knock", tier: .bulk,
              strikes: [Strike(at: 0, freqs: [420, 840, 1260], dur: 0.05, amp: 0.55)],
              seconds: 0.12),

    /// Higher and drier than the knock — a fingernail rather than a knuckle,
    /// for a pile being moved fast enough that the knock starts to feel heavy.
    VoiceSpec(id: "tick", label: "Tick", tier: .bulk,
              strikes: [Strike(at: 0, freqs: [1400, 2800], dur: 0.03, amp: 0.45)],
              seconds: 0.08),

    /// Below the knock, and softer. Nearly felt rather than heard, for long
    /// sittings where any bright bulk sound wears through.
    VoiceSpec(id: "thump", label: "Thump", tier: .bulk,
              strikes: [Strike(at: 0, freqs: [180, 360, 540], dur: 0.07, amp: 0.60)],
              seconds: 0.16),

    // MARK: Win — one bright event. Something happened; keep going.

    /// A single bright service bell — fundamental plus an inharmonic 2.76x
    /// partial, which is what reads as "bell" rather than "tone".
    VoiceSpec(id: "bell", label: "Bell", tier: .win,
              strikes: [Strike(at: 0, freqs: [2093, 5777], dur: 0.4, amp: 0.42)],
              seconds: 0.55),

    /// The same bell recipe struck a fourth lower — rounder and less piercing
    /// over a long session, still unmistakably the same instrument.
    VoiceSpec(id: "chime", label: "Chime", tier: .win,
              strikes: [Strike(at: 0, freqs: [1568, 4327], dur: 0.45, amp: 0.40)],
              seconds: 0.6),

    /// Deliberately *not* a bell: one pure partial, no inharmonic, short decay.
    /// Dropping the 2.76x partial is exactly what turns it from bell into tone,
    /// so it reads as a marker rather than a celebration.
    VoiceSpec(id: "ping", label: "Ping", tier: .win,
              strikes: [Strike(at: 0, freqs: [2637], dur: 0.22, amp: 0.40)],
              seconds: 0.32),

    // MARK: Big Win — two or more events, rising. Set it aside.

    /// Two bells rising — the same inharmonic 2.76x recipe as the single bell,
    /// struck a fourth apart, so it reads as the bell's bigger sibling rather
    /// than a new instrument.
    VoiceSpec(id: "bells", label: "Double Bell", tier: .big,
              strikes: [Strike(at: 0.00, freqs: [1568, 4329], dur: 0.3, amp: 0.38),
                        Strike(at: 0.17, freqs: [2093, 5777], dur: 0.45, amp: 0.42)],
              seconds: 0.75),

    /// A quick major arpeggio landing on a held octave — smaller than the harp
    /// run, but unmistakably a fanfare.
    VoiceSpec(id: "arpeggio", label: "Fanfare", tier: .big,
              strikes: [Strike(at: 0.00, freqs: [1046.5], dur: 0.14, amp: 0.34),
                        Strike(at: 0.09, freqs: [1318.5], dur: 0.14, amp: 0.34),
                        Strike(at: 0.18, freqs: [1568.0], dur: 0.14, amp: 0.34),
                        Strike(at: 0.27, freqs: [2093.0, 1046.5], dur: 0.5, amp: 0.42)],
              seconds: 0.9),

    /// One chord opening into a wider one — no melody to follow, just the room
    /// getting bigger. The quietest way to say "this one is worth money".
    VoiceSpec(id: "swell", label: "Swell", tier: .big,
              strikes: [Strike(at: 0.00, freqs: [1046.5, 2093.0], dur: 0.18, amp: 0.38),
                        Strike(at: 0.14, freqs: [1318.5, 1568.0, 2637.0], dur: 0.55, amp: 0.44)],
              seconds: 0.8),

    // MARK: Jackpot — long and multi-note. Stop and look.

    /// A pentatonic sweep, two octaves in under a second, landing on a held
    /// octave chord — a harp run up to the top of the machine. The offsets are
    /// fixed, never random: the sound being identical every time is what makes
    /// it recognizable.
    VoiceSpec(id: "harp", label: "Harp Run", tier: .jackpot,
              strikes: [523.25, 587.33, 659.25, 783.99, 880.0,
                        1046.5, 1174.7, 1318.5, 1568.0, 1760.0]
                  .enumerated()
                  .map { Strike(at: Double($0.offset) * 0.055, freqs: [$0.element],
                                dur: 0.12, amp: 0.32) }
                  + [Strike(at: 0.62, freqs: [2093.0, 1046.5, 4186.0], dur: 0.9, amp: 0.48)],
              seconds: 1.9),

    /// A four-note call that turns back on itself before landing on the triad —
    /// the shape of a bugle rather than a sweep. Shorter than the harp run, for
    /// a jackpot line set low enough that it trips more than once a box.
    VoiceSpec(id: "fanfare", label: "Brass Call", tier: .jackpot,
              strikes: [Strike(at: 0.00, freqs: [783.99], dur: 0.12, amp: 0.36),
                        Strike(at: 0.10, freqs: [1046.5], dur: 0.12, amp: 0.36),
                        Strike(at: 0.20, freqs: [1318.5], dur: 0.12, amp: 0.36),
                        Strike(at: 0.30, freqs: [1046.5], dur: 0.10, amp: 0.32),
                        Strike(at: 0.40, freqs: [1568.0, 1046.5, 783.99],
                               dur: 0.8, amp: 0.46)],
              seconds: 1.4),

    /// The harp run's mirror: the notes fall instead of climbing, then bloom
    /// into a chord anchored an octave below where the harp lands. Same length
    /// of event, opposite gesture, so the two are never confused for each other.
    VoiceSpec(id: "cascade", label: "Cascade", tier: .jackpot,
              strikes: [2093.0, 1760.0, 1568.0, 1318.5, 1046.5, 880.0, 783.99, 659.25]
                  .enumerated()
                  .map { Strike(at: Double($0.offset) * 0.05, freqs: [$0.element],
                                dur: 0.11, amp: 0.30) }
                  + [Strike(at: 0.45, freqs: [523.25, 1046.5, 1568.0, 2093.0],
                            dur: 1.0, amp: 0.50)],
              seconds: 1.6),

    // MARK: Review — a question, never an outcome.
    //
    // These three all end unresolved, on purpose. A review sound that lands
    // like a price would send someone to the keep pile for a card the Mac is
    // still asking about, which is the one mistake this tier can cause.

    /// Two soft notes rising a fourth — "hm-hmm?" — the upward inflection of a
    /// question. A queued card is a request, not a price outcome.
    VoiceSpec(id: "question", label: "Question", tier: .review,
              strikes: [Strike(at: 0.00, freqs: [440, 880], dur: 0.12, amp: 0.34),
                        Strike(at: 0.16, freqs: [587.33, 1174.7], dur: 0.28, amp: 0.38)],
              seconds: 0.6),

    /// Three steps up and no landing — "hm-hm-hmm?". More insistent than the
    /// question, for a session where reviews need to interrupt.
    VoiceSpec(id: "query", label: "Query", tier: .review,
              strikes: [Strike(at: 0.00, freqs: [523.25], dur: 0.10, amp: 0.32),
                        Strike(at: 0.11, freqs: [587.33], dur: 0.10, amp: 0.32),
                        Strike(at: 0.22, freqs: [698.46, 1396.9], dur: 0.26, amp: 0.36)],
              seconds: 0.6),

    /// The same note twice, then a lift — a tap on the shoulder before the
    /// question. The softest of the three, for working next to other people.
    VoiceSpec(id: "nudge", label: "Nudge", tier: .review,
              strikes: [Strike(at: 0.00, freqs: [392.0, 784.0], dur: 0.14, amp: 0.30),
                        Strike(at: 0.15, freqs: [392.0, 784.0], dur: 0.14, amp: 0.28),
                        Strike(at: 0.32, freqs: [523.25, 1046.5], dur: 0.30, amp: 0.34)],
              seconds: 0.7),
]

/// Sounds plays one voice per resolved card.
@MainActor
final class Sounds {
    /// Every voice, in picker order. Derived from the table, so it can never
    /// name a voice the renderer does not know.
    static let voices: [SoundVoice] = voiceTable.map {
        SoundVoice(id: $0.id, label: $0.label, tier: $0.tier)
    }

    /// The id of a tier that has been turned off.
    ///
    /// Silence is a choice, so it is a *member of every tier's palette* rather
    /// than a value sitting outside the vocabulary. That membership is what
    /// keeps it apart from a dangling id: TierSettings repairs a stored id its
    /// tier does not offer, and this one is offered by every tier, so it
    /// survives a relaunch where `""` or an invented `"none"` would be
    /// mistaken for a retired voice and quietly replaced by the default.
    ///
    /// The value cannot collide with a real voice — the whole vocabulary is the
    /// table above, and nothing in it is named this.
    static let silence = "silent"

    /// What one tier's picker offers: that tier's default first, silence last.
    /// A voice belongs to exactly one tier, so the sounding voices partition
    /// `voices` with no overlap — what Bulk can say, Jackpot cannot. Silence is
    /// the single exception, and is deliberately shared by every tier: turning
    /// one off has to be as available as any other choice, and turning all five
    /// off is a mute app.
    static func voices(for tier: Tier) -> [SoundVoice] {
        voices.filter { $0.tier == tier }
            + [SoundVoice(id: silence, label: "Silent", tier: tier)]
    }

    private let engine = AVAudioEngine()
    private let player = AVAudioPlayerNode()
    private var buffers: [String: AVAudioPCMBuffer] = [:]
    private var working = false

    init() {
        let format = AVAudioFormat(standardFormatWithSampleRate: 44100, channels: 2)!
        engine.attach(player)
        engine.connect(player, to: engine.mainMixerNode, format: format)

        for spec in voiceTable {
            if let buf = render(spec.strikes, seconds: spec.seconds, format: format) {
                buffers[spec.id] = buf
            }
        }

        do {
            // .playback, not .ambient — and this is the difference between
            // working and silent. `.ambient` is muted by the ring/silent switch,
            // and a phone that has been sitting in a stand pointed at a desk is
            // very likely on silent. The sound *is* the feature here: it is what
            // lets a person keep their eyes on the cards, so it has to play
            // whether or not the switch is flipped, the same as any media app.
            //
            // `.mixWithOthers` keeps it polite: music the user already had on
            // keeps playing, and the tier voices land on top.
            try AVAudioSession.sharedInstance().setCategory(
                .playback, mode: .default, options: [.mixWithOthers])
            try AVAudioSession.sharedInstance().setActive(true)
            try engine.start()
            player.play()
            working = true
            status = "Ready"
        } catch {
            // Survivable: the price still shows on screen. But it must be
            // *visible* that audio is off, or a silent session looks like a
            // scanner that stopped working.
            working = false
            SessionLog.write("audio session failed: \(error.localizedDescription)")
            status = "No sound. Check the phone is not on silent"
        }
        observeSessionLife()
    }

    /// observeSessionLife keeps the engine honest across the session's life.
    ///
    /// Nothing observed these before, so the first phone call or Bluetooth
    /// speaker wandering out of range killed audio for the rest of the sitting
    /// while `isWorking` went on reporting healthy — the "sound is broken"
    /// warning on the scanning screen, built for exactly this, never showed.
    /// A person working by ear does not notice silence until cards later.
    private func observeSessionLife() {
        let nc = NotificationCenter.default
        nc.addObserver(forName: AVAudioSession.interruptionNotification,
                       object: nil, queue: .main) { [weak self] note in
            // Read out of the notification before the actor hop; the
            // Notification itself is not Sendable.
            let type = (note.userInfo?[AVAudioSessionInterruptionTypeKey] as? UInt)
                .flatMap(AVAudioSession.InterruptionType.init)
            Task { @MainActor [weak self] in
                guard let self else { return }
                switch type {
                case .began:
                    // The engine is already stopped by the system; say so, so
                    // the warning shows during the call rather than after it.
                    self.working = false
                    self.status = "Sound paused by a call or another app"
                case .ended, nil:
                    // `.shouldResume` is deliberately not consulted: it means
                    // "polite to resume", and a scanner whose whole feedback
                    // channel is audio is the case politeness defers to.
                    self.restartEngine()
                @unknown default:
                    break
                }
            }
        }
        nc.addObserver(forName: AVAudioSession.routeChangeNotification,
                       object: nil, queue: .main) { [weak self] _ in
            // Any route change: AirPods connecting, a cable pulled, Bluetooth
            // dropping. The engine stops itself on most of these and does not
            // restart on its own; a blanket restart is cheap and idempotent.
            Task { @MainActor [weak self] in self?.restartEngine() }
        }
    }

    /// restartEngine brings the session and engine back after an interruption
    /// or route change, and records honestly when it could not.
    private func restartEngine() {
        do {
            try AVAudioSession.sharedInstance().setActive(true)
            if !engine.isRunning { try engine.start() }
            player.play()
            working = true
            status = "Ready"
        } catch {
            working = false
            SessionLog.write("audio restart failed: \(error.localizedDescription)")
            status = "No sound. Check the phone is not on silent"
        }
    }

    /// Why sound is or is not playing, for the session screen.
    private(set) var status = "Starting"

    /// The engine's real state, not init's memory of it. `working` alone went
    /// stale the moment anything stopped the engine from outside — which is
    /// exactly when the scanning screen needs this to read false.
    var isWorking: Bool { working && engine.isRunning }

    /// play cuts whatever tail is still ringing and starts the voice.
    /// A rapid next card should clip the last fanfare, not queue behind it.
    func play(voice: String) {
        // A tier turned off in Settings arrives here as `silence` and is meant
        // to make no sound. Refused by name, above everything else, rather than
        // left to fall out of the buffer lookup below: that lookup also
        // swallows an id no voice answers to, and a tier the person muted must
        // not take the same path as a tier that lost its voice. It is also the
        // reason a silent tier does not restart the engine — nothing is about
        // to be played, so there is nothing to heal.
        guard voice != Sounds.silence else { return }
        // One self-heal before giving up: an engine stopped behind our back
        // (a route change this run missed, a media-services reset) restarts
        // in a few milliseconds, and a card's sound is worth that try.
        if working, !engine.isRunning { restartEngine() }
        guard working, let buffer = buffers[voice] else { return }
        // Schedule first, then play. Doing it the other way round races: `stop()`
        // resets the node's render state, and a buffer handed to a node that has
        // not been restarted yet can be dropped on the floor.
        player.stop()
        player.scheduleBuffer(buffer, at: nil, options: .interrupts)
        player.play()
    }

    /// render sums the strikes into one buffer, with an exponential decay and a
    /// soft clip so overlapping notes saturate rather than wrap.
    private func render(_ strikes: [Strike], seconds: Double,
                        format: AVAudioFormat) -> AVAudioPCMBuffer? {
        let rate = format.sampleRate
        let count = AVAudioFrameCount(seconds * rate)
        guard let buffer = AVAudioPCMBuffer(pcmFormat: format, frameCapacity: count) else {
            return nil
        }
        buffer.frameLength = count
        guard let channels = buffer.floatChannelData else { return nil }

        var mono = [Float](repeating: 0, count: Int(count))
        for strike in strikes {
            let start = Int(strike.at * rate)
            let length = min(Int(strike.dur * rate), Int(count) - start)
            guard length > 0 else { continue }
            let tau = strike.dur / 5
            for i in 0..<length {
                let t = Double(i) / rate
                let envelope = exp(-t / tau)
                var sample = 0.0
                for f in strike.freqs { sample += sin(2 * .pi * f * t) }
                mono[start + i] += Float(sample / Double(strike.freqs.count)
                    * envelope * strike.amp)
            }
        }
        for i in 0..<Int(count) {
            let clipped = tanh(mono[i])
            channels[0][i] = clipped
            channels[1][i] = clipped
        }
        return buffer
    }
}
