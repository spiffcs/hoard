// The price tiers' voices, on the phone.
//
// Synthesis parameters are copied deliberately and exactly from the macOS
// helper's SoundBank, because the sound *is* the feature — the whole point of
// one sound per card is that a person stops looking at the screen and works by
// ear, and a voice that differs between the two scan sources retrains them for
// nothing. Same frequencies, same durations, same decay, same soft clip.
//
// Everything is synthesized into PCM buffers once at init — additive sine bursts
// with exponential decay — so the app bundles no audio files and owes no one a
// license.
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

/// The four voices, at the macOS helper's measured parameters.
private enum Voice {
    /// A low woody knock, gone in 50 ms.
    static let bulk = ([Strike(at: 0, freqs: [420, 840, 1260], dur: 0.05, amp: 0.55)], 0.12)

    /// A single bright service bell — fundamental plus an inharmonic 2.76x
    /// partial, which is what reads as "bell" rather than "tone".
    static let win = ([Strike(at: 0, freqs: [2093, 5777], dur: 0.4, amp: 0.42)], 0.55)

    /// A pentatonic sweep, two octaves in under a second, landing on a held
    /// octave chord — a harp run up to the top of the machine. The offsets are
    /// fixed, never random: the sound being identical every time is what makes
    /// it recognizable.
    static let jackpot: ([Strike], Double) = (
        [523.25, 587.33, 659.25, 783.99, 880.0, 1046.5, 1174.7, 1318.5, 1568.0, 1760.0]
            .enumerated()
            .map { Strike(at: Double($0.offset) * 0.055, freqs: [$0.element], dur: 0.12, amp: 0.32) }
            + [Strike(at: 0.62, freqs: [2093.0, 1046.5, 4186.0], dur: 0.9, amp: 0.48)],
        1.9)

    /// Two soft notes rising a fourth — "hm-hmm?" — the upward inflection of a
    /// question. A queued card is a request, not a price outcome.
    static let review = (
        [Strike(at: 0.00, freqs: [440, 880], dur: 0.12, amp: 0.34),
         Strike(at: 0.16, freqs: [587.33, 1174.7], dur: 0.28, amp: 0.38)],
        0.6)
}

/// Sounds plays one voice per resolved card.
@MainActor
final class Sounds {
    private let engine = AVAudioEngine()
    private let player = AVAudioPlayerNode()
    private var buffers: [String: AVAudioPCMBuffer] = [:]
    private var working = false

    init() {
        let format = AVAudioFormat(standardFormatWithSampleRate: 44100, channels: 2)!
        engine.attach(player)
        engine.connect(player, to: engine.mainMixerNode, format: format)

        for (tier, spec) in [
            ("bulk", Voice.bulk), ("win", Voice.win),
            ("jackpot", Voice.jackpot), ("review", Voice.review),
        ] {
            if let buf = render(spec.0, seconds: spec.1, format: format) {
                buffers[tier] = buf
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
    }

    /// Why sound is or is not playing, for the session screen.
    private(set) var status = "Starting"

    var isWorking: Bool { working }

    /// play cuts whatever tail is still ringing and starts the tier's sound.
    /// A rapid next card should clip the last fanfare, not queue behind it.
    func play(tier: String) {
        guard working, let buffer = buffers[tier] else { return }
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
