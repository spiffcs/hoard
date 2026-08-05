import AVFoundation
import CoreGraphics
import Foundation

/// FocusPolicy owns the lens.
///
/// A scanning session is an unusual thing to point a camera at: every card sits
/// at the same distance, one after another, and the only thing that changes is
/// which card it is. Continuous autofocus treats each landing card as a reason
/// to hunt, and a hunt costs settle time on every single scan while blurring
/// the frames the trigger is trying to read stillness out of.
///
/// So the policy is: focus once, freeze there, and thaw only on evidence that
/// the rig itself moved — two consecutive captures that read nothing.
/// HOARD_SCAN_FOCUS selects it: "lock" does the above, "continuous" keeps the
/// hunt-aware fire gate without freezing, and "off" is the pre-focus-management
/// behaviour byte for byte.
///
/// Main-thread only, like the rest of the controller's state.
final class FocusPolicy {
    private weak var device: AVCaptureDevice?
    private var observation: NSKeyValueObservation?
    private var hunting = false
    private var huntBegan: Date?
    private var locked = false
    private var emptyReads = 0

    /// The camera's word that the lens is mid-hunt. A hunt blurs every edge in
    /// frame, so the trigger treats those samples as noise rather than motion
    /// and defers its fire until the hunt ends.
    var isHunting: Bool { hunting }

    /// start points continuous autofocus at where cards land and begins
    /// watching the lens. HOARD_SCAN_FOCUS=off skips all of it.
    func start(on device: AVCaptureDevice) {
        guard TriggerTuning.focusControl != "off" else { return }
        self.device = device
        do {
            try device.lockForConfiguration()
            if device.isFocusPointOfInterestSupported {
                device.focusPointOfInterest = CGPoint(x: 0.5, y: 0.5)
            }
            if device.isFocusModeSupported(.continuousAutoFocus) {
                device.focusMode = .continuousAutoFocus
            }
            device.unlockForConfiguration()
        } catch {
            autoDebug("focus setup refused: \(error.localizedDescription)")
        }
        observation = device.observe(\.isAdjustingFocus, options: [.new]) { [weak self] dev, _ in
            let hunting = dev.isAdjustingFocus
            DispatchQueue.main.async {
                guard let self, self.hunting != hunting else { return }
                self.hunting = hunting
                if hunting {
                    self.huntBegan = Date()
                    autoDebug("focus hunt began")
                } else if let t = self.huntBegan {
                    autoDebug("focus hunt ended (\(msSince(t))ms)")
                    self.huntBegan = nil
                }
            }
        }
    }

    /// update freezes the lens after a capture that actually read a card, and
    /// thaws it after two consecutive empty reads — the signature of a moved
    /// rig. Only the "lock" policy does any of this.
    func update(afterGoodRead good: Bool) {
        guard TriggerTuning.focusControl == "lock", let device else { return }
        if good {
            emptyReads = 0
            guard !locked, device.isFocusModeSupported(.locked) else { return }
            do {
                try device.lockForConfiguration()
                device.focusMode = .locked
                device.unlockForConfiguration()
                locked = true
                autoDebug("focus locked after a good read")
            } catch {
                autoDebug("focus lock refused: \(error.localizedDescription)")
            }
        } else if locked {
            emptyReads += 1
            guard emptyReads >= 2,
                  device.isFocusModeSupported(.continuousAutoFocus) else { return }
            do {
                try device.lockForConfiguration()
                device.focusMode = .continuousAutoFocus
                device.unlockForConfiguration()
                locked = false
                emptyReads = 0
                autoDebug("focus unlocked after consecutive empty reads")
            } catch {
                autoDebug("focus unlock refused: \(error.localizedDescription)")
            }
        }
    }

    /// restore hands the camera back hunting normally for whatever app uses it
    /// next. A frozen lens is session state, not a setting to leave behind.
    func restore() {
        guard locked, let device, device.isFocusModeSupported(.continuousAutoFocus),
              (try? device.lockForConfiguration()) != nil else { return }
        device.focusMode = .continuousAutoFocus
        device.unlockForConfiguration()
    }
}
