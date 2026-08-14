import AVFoundation
import Foundation

private struct Strike {
    let at: Double
    let freqs: [Double]
    let dur: Double
    let amp: Double
}

struct SoundVoice: Identifiable, Equatable {
    let id: String
    let label: String
    let tier: Tier
}

private struct VoiceSpec {
    let id: String
    let label: String
    let tier: Tier
    let strikes: [Strike]
    let seconds: Double
}

private let voiceTable: [VoiceSpec] = [

    VoiceSpec(id: "knock", label: "Knock", tier: .bulk,
              strikes: [Strike(at: 0, freqs: [420, 840, 1260], dur: 0.05, amp: 0.55)],
              seconds: 0.12),

    VoiceSpec(id: "tick", label: "Tick", tier: .bulk,
              strikes: [Strike(at: 0, freqs: [1400, 2800], dur: 0.03, amp: 0.45)],
              seconds: 0.08),

    VoiceSpec(id: "thump", label: "Thump", tier: .bulk,
              strikes: [Strike(at: 0, freqs: [180, 360, 540], dur: 0.07, amp: 0.60)],
              seconds: 0.16),

    VoiceSpec(id: "bell", label: "Bell", tier: .win,
              strikes: [Strike(at: 0, freqs: [2093, 5777], dur: 0.4, amp: 0.42)],
              seconds: 0.55),

    VoiceSpec(id: "chime", label: "Chime", tier: .win,
              strikes: [Strike(at: 0, freqs: [1568, 4327], dur: 0.45, amp: 0.40)],
              seconds: 0.6),

    VoiceSpec(id: "ping", label: "Ping", tier: .win,
              strikes: [Strike(at: 0, freqs: [2637], dur: 0.22, amp: 0.40)],
              seconds: 0.32),

    VoiceSpec(id: "bells", label: "Double Bell", tier: .big,
              strikes: [Strike(at: 0.00, freqs: [1568, 4329], dur: 0.3, amp: 0.38),
                        Strike(at: 0.17, freqs: [2093, 5777], dur: 0.45, amp: 0.42)],
              seconds: 0.75),

    VoiceSpec(id: "arpeggio", label: "Fanfare", tier: .big,
              strikes: [Strike(at: 0.00, freqs: [1046.5], dur: 0.14, amp: 0.34),
                        Strike(at: 0.09, freqs: [1318.5], dur: 0.14, amp: 0.34),
                        Strike(at: 0.18, freqs: [1568.0], dur: 0.14, amp: 0.34),
                        Strike(at: 0.27, freqs: [2093.0, 1046.5], dur: 0.5, amp: 0.42)],
              seconds: 0.9),

    VoiceSpec(id: "swell", label: "Swell", tier: .big,
              strikes: [Strike(at: 0.00, freqs: [1046.5, 2093.0], dur: 0.18, amp: 0.38),
                        Strike(at: 0.14, freqs: [1318.5, 1568.0, 2637.0], dur: 0.55, amp: 0.44)],
              seconds: 0.8),

    VoiceSpec(id: "harp", label: "Harp Run", tier: .jackpot,
              strikes: [523.25, 587.33, 659.25, 783.99, 880.0,
                        1046.5, 1174.7, 1318.5, 1568.0, 1760.0]
                  .enumerated()
                  .map { Strike(at: Double($0.offset) * 0.055, freqs: [$0.element],
                                dur: 0.12, amp: 0.32) }
                  + [Strike(at: 0.62, freqs: [2093.0, 1046.5, 4186.0], dur: 0.9, amp: 0.48)],
              seconds: 1.9),

    VoiceSpec(id: "fanfare", label: "Brass Call", tier: .jackpot,
              strikes: [Strike(at: 0.00, freqs: [783.99], dur: 0.12, amp: 0.36),
                        Strike(at: 0.10, freqs: [1046.5], dur: 0.12, amp: 0.36),
                        Strike(at: 0.20, freqs: [1318.5], dur: 0.12, amp: 0.36),
                        Strike(at: 0.30, freqs: [1046.5], dur: 0.10, amp: 0.32),
                        Strike(at: 0.40, freqs: [1568.0, 1046.5, 783.99],
                               dur: 0.8, amp: 0.46)],
              seconds: 1.4),

    VoiceSpec(id: "cascade", label: "Cascade", tier: .jackpot,
              strikes: [2093.0, 1760.0, 1568.0, 1318.5, 1046.5, 880.0, 783.99, 659.25]
                  .enumerated()
                  .map { Strike(at: Double($0.offset) * 0.05, freqs: [$0.element],
                                dur: 0.11, amp: 0.30) }
                  + [Strike(at: 0.45, freqs: [523.25, 1046.5, 1568.0, 2093.0],
                            dur: 1.0, amp: 0.50)],
              seconds: 1.6),

    VoiceSpec(id: "question", label: "Question", tier: .review,
              strikes: [Strike(at: 0.00, freqs: [440, 880], dur: 0.12, amp: 0.34),
                        Strike(at: 0.16, freqs: [587.33, 1174.7], dur: 0.28, amp: 0.38)],
              seconds: 0.6),

    VoiceSpec(id: "query", label: "Query", tier: .review,
              strikes: [Strike(at: 0.00, freqs: [523.25], dur: 0.10, amp: 0.32),
                        Strike(at: 0.11, freqs: [587.33], dur: 0.10, amp: 0.32),
                        Strike(at: 0.22, freqs: [698.46, 1396.9], dur: 0.26, amp: 0.36)],
              seconds: 0.6),

    VoiceSpec(id: "nudge", label: "Nudge", tier: .review,
              strikes: [Strike(at: 0.00, freqs: [392.0, 784.0], dur: 0.14, amp: 0.30),
                        Strike(at: 0.15, freqs: [392.0, 784.0], dur: 0.14, amp: 0.28),
                        Strike(at: 0.32, freqs: [523.25, 1046.5], dur: 0.30, amp: 0.34)],
              seconds: 0.7),
]

@MainActor
final class Sounds {
    static let voices: [SoundVoice] = voiceTable.map {
        SoundVoice(id: $0.id, label: $0.label, tier: $0.tier)
    }

    static let silence = "silent"

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
            try AVAudioSession.sharedInstance().setCategory(
                .playback, mode: .default, options: [.mixWithOthers])
            try AVAudioSession.sharedInstance().setActive(true)
            try engine.start()
            player.play()
            working = true
            status = "Ready"
        } catch {
            working = false
            SessionLog.write("audio session failed: \(error.localizedDescription)")
            status = "No sound. Check the phone is not on silent"
        }
        observeSessionLife()
    }

    private func observeSessionLife() {
        let nc = NotificationCenter.default
        nc.addObserver(forName: AVAudioSession.interruptionNotification,
                       object: nil, queue: .main) { [weak self] note in
            let type = (note.userInfo?[AVAudioSessionInterruptionTypeKey] as? UInt)
                .flatMap(AVAudioSession.InterruptionType.init)
            Task { @MainActor [weak self] in
                guard let self else { return }
                switch type {
                case .began:
                    self.working = false
                    self.status = "Sound paused by a call or another app"
                case .ended, nil:
                    self.restartEngine()
                @unknown default:
                    break
                }
            }
        }
        nc.addObserver(forName: AVAudioSession.routeChangeNotification,
                       object: nil, queue: .main) { [weak self] _ in
            Task { @MainActor [weak self] in self?.restartEngine() }
        }
    }

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

    private(set) var status = "Starting"

    var isWorking: Bool { working && engine.isRunning }

    func play(voice: String) {
        guard voice != Sounds.silence else { return }
        if working, !engine.isRunning { restartEngine() }
        guard working, let buffer = buffers[voice] else { return }
        player.stop()
        player.scheduleBuffer(buffer, at: nil, options: .interrupts)
        player.play()
    }

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
