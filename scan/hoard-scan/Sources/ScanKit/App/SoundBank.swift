import AVFoundation
import AppKit

// MARK: - Sound synthesis

/// SoundBank plays the price tiers' casino sounds — a low woody knock for
/// bulk, a bright service bell for a win, and a harp-run glissando for a
/// jackpot (the owner's picks from the 2026-08 audition of synthesized
/// candidates). Everything is synthesized into PCM buffers once at init —
/// additive sine bursts with exponential decay — so the app bundles no audio
/// files and owes no one a license. Engine failure — no output device,
/// aggregate-device weirdness — degrades to the system Glass chime, never to
/// silence.
///
/// A queued card gets its own voice — a soft two-note rise, the sound of a
/// question being asked — because review is a request ("is this right?"),
/// not a price outcome.
///
/// A tier's sound can also be replaced outright: HOARD_SCAN_SOUND_BULK /
/// _WIN / _JACKPOT / _REVIEW each take a path to an audio file (anything
/// NSSound reads — wav, aiff, mp3, m4a), for users who film or publish
/// their scanning sessions and need audio they hold a license to
/// distribute. An unreadable path reports one error event and falls back to
/// the synth.
///
/// Main-thread only, like the rest of the controller's state.
final class SoundBank {
    private let engine = AVAudioEngine()
    private let player = AVAudioPlayerNode()
    private var buffers: [String: AVAudioPCMBuffer] = [:]
    /// User-supplied replacements by tier, loaded from the env; these win
    /// over the synth buffers and play even when the engine failed.
    private var custom: [String: NSSound] = [:]
    /// The custom sound last started, stopped before the next play so rapid
    /// scans cut tails exactly like the engine path's player.stop().
    private var playing: NSSound?
    private var ok = false

    /// One synthesis event: freqs sound together from t for dur seconds,
    /// decaying exponentially — a struck, ringing thing.
    private typealias Strike = (t: Double, freqs: [Double], dur: Double, amp: Double)

    init() {
        let env = ProcessInfo.processInfo.environment
        let volume = Float(max(0, min(1, envDouble("HOARD_SCAN_HUD_VOLUME", 1.0))))
        for tier in ["bulk", "win", "jackpot", "review"] {
            guard let path = env["HOARD_SCAN_SOUND_\(tier.uppercased())"], !path.isEmpty else {
                continue
            }
            if let snd = NSSound(contentsOf: URL(fileURLWithPath: path), byReference: true) {
                snd.volume = volume
                custom[tier] = snd
            } else {
                emit(Event(event: "error",
                           message: "could not load \(tier) sound \(path); using the built-in"))
            }
        }
        let format = AVAudioFormat(standardFormatWithSampleRate: 44_100, channels: 2)
        guard let format else { return }
        engine.attach(player)
        engine.connect(player, to: engine.mainMixerNode, format: format)
        engine.mainMixerNode.outputVolume = Float(envDouble("HOARD_SCAN_HUD_VOLUME", 1.0))
        do {
            try engine.start()
        } catch {
            return
        }
        // Bulk: a low woody knock, gone in 50ms.
        buffers["bulk"] = render(format, [(0, [420, 840, 1260], 0.05, 0.55)], length: 0.12)
        // Win: a single bright service bell (fundamental plus an inharmonic
        // 2.76× partial, which is what reads as "bell" rather than "tone").
        buffers["win"] = render(format, [(0, [2093, 5777], 0.4, 0.42)], length: 0.55)
        // Jackpot: a pentatonic sweep, two octaves in under a second, landing
        // on a held octave chord — a harp run up to the top of the machine.
        // Offsets are fixed, never random: the sound being identical every
        // time is what makes it recognizable.
        var gliss: [Strike] = []
        let penta = [523.25, 587.33, 659.25, 783.99, 880.0,
                     1046.5, 1174.7, 1318.5, 1568.0, 1760.0] // C D E G A ×2
        for (i, f) in penta.enumerated() {
            gliss.append((Double(i) * 0.055, [f], 0.12, 0.32))
        }
        gliss.append((0.62, [2093.0, 1046.5, 4186.0], 0.9, 0.48))
        buffers["jackpot"] = render(format, gliss, length: 1.9)
        // Review: two soft notes rising a fourth — "hm-hmm?" — the upward
        // inflection of a question. Warm low partials (f + 2f) keep it
        // marimba-ish and unhurried, nothing like the win bell's brightness.
        buffers["review"] = render(format, [
            (0.00, [440, 880], 0.12, 0.34),   // A4
            (0.16, [587.33, 1174.7], 0.28, 0.38), // D5, held — the "?"
        ], length: 0.6)
        ok = true
    }

    /// render sums the strikes into one stereo buffer, soft-clipped so
    /// overlapping notes saturate rather than wrap.
    private func render(_ format: AVAudioFormat, _ strikes: [Strike],
                        length: Double) -> AVAudioPCMBuffer? {
        let sr = format.sampleRate
        let frames = AVAudioFrameCount(length * sr)
        guard let buf = AVAudioPCMBuffer(pcmFormat: format, frameCapacity: frames),
              let channels = buf.floatChannelData else { return nil }
        buf.frameLength = frames
        let left = channels[0], right = channels[1]
        for s in strikes {
            let start = Int(s.t * sr)
            let count = Int(s.dur * sr)
            let tau = s.dur / 5
            for i in 0..<count where start + i < Int(frames) {
                let t = Double(i) / sr
                var v = 0.0
                for f in s.freqs { v += sin(2 * .pi * f * t) }
                v *= s.amp * exp(-t / tau) / Double(s.freqs.count)
                left[start + i] += Float(v)
                right[start + i] += Float(v)
            }
        }
        for i in 0..<Int(frames) {
            left[i] = tanh(left[i])
            right[i] = tanh(right[i])
        }
        return buf
    }

    /// play cuts whatever tail is still ringing and starts the tier's sound —
    /// a rapid next card should clip the last fanfare, not queue behind it.
    /// Unknown tiers and the unpriced shrug keep the familiar Glass chime.
    func play(tier: String) {
        // Both paths stop first, whichever played last: tails never stack.
        playing?.stop()
        player.stop()
        if let snd = custom[tier] {
            playing = snd
            snd.play()
            return
        }
        guard ok, let buf = buffers[tier] else {
            NSSound(named: "Glass")?.play()
            return
        }
        player.scheduleBuffer(buf)
        player.play()
    }
}
