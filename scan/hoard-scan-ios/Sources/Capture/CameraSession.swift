import AVFoundation
import CardKit
import Foundation
import UIKit

@MainActor
final class CameraSession: NSObject, ObservableObject {
    let session = AVCaptureSession()
    private(set) var device: AVCaptureDevice?

    @Published var status = "Starting…"

    @Published private(set) var failure = ""
    @Published var lensPosition: Float = 0.5
    @Published var torchLevel: Float = 0
    @Published var locked = false
    @Published var lastCapture: CapturedFrame?
    @Published var busy = false

    private let lensType = AVCaptureDevice.DeviceType.builtInWideAngleCamera

    @Published var previewRotation: CGFloat = 90

    private var rotationCoordinator: AVCaptureDevice.RotationCoordinator?
    private var rotationObservation: NSKeyValueObservation?

    private let triggerRunner = TriggerRunner()
    var onFire: (@MainActor () -> Void)? {
        get { triggerRunner.onFire }
        set { triggerRunner.onFire = newValue }
    }
    var onPhase: (@MainActor (TriggerPhase) -> Void)? {
        get { triggerRunner.onPhase }
        set { triggerRunner.onPhase = newValue }
    }
    var onCue: (@MainActor (CGRect?) -> Void)? {
        get { triggerRunner.onBox }
        set { triggerRunner.onBox = newValue }
    }

    var onTriggerTrace: (@MainActor (String) -> Void)? {
        get { triggerRunner.onTrace }
        set { triggerRunner.onTrace = newValue }
    }
    private(set) var autoAvailable = false

    @Published var autoOn = false
    var autoUnavailableReason: String { triggerRunner.unavailableReason }

    func toggleAuto() {
        autoOn ? stopTrigger() : startTrigger()
    }

    var tapSize: CGSize { triggerRunner.bufferSize }

    private static let captureGrace: TimeInterval = 2

    private func captureFromVideo() async -> CapturedFrame? {
        await withCheckedContinuation { (c: CheckedContinuation<CapturedFrame?, Never>) in
            let resumed = Flag()
            triggerRunner.grabFrame { buffer in
                guard !resumed.testAndSet() else { return }
                let ci = CIImage(cvPixelBuffer: buffer).oriented(.right)
                guard let cg = sharedContext.createCGImage(ci, from: ci.extent) else {
                    c.resume(returning: nil)
                    return
                }
                c.resume(returning: CapturedFrame(image: cg, orientation: .up))
            }
            DispatchQueue.global().asyncAfter(deadline: .now() + Self.captureGrace) {
                [weak triggerRunner] in
                guard !resumed.testAndSet() else { return }
                triggerRunner?.cancelGrab()
                c.resume(returning: nil)
            }
        }
    }

    func startTrigger() {
        guard autoAvailable else { return }
        autoOn = true
        triggerRunner.start()
    }
    func stopTrigger() {
        autoOn = false
        triggerRunner.stop()
    }
    func nudgeTrigger() { triggerRunner.nudge() }
    func rearmForResult() { triggerRunner.rearmForResult() }
    var fireCause: String? {
        let c = triggerRunner.lastFireCause
        return c == .none ? nil : c.rawValue
    }
    var fireHoldDelta: Double? { triggerRunner.lastFireHoldDelta }
    var fireFaceDelta: Double? { triggerRunner.lastFireFaceDelta }
    func tuneTrigger(stable: Int, interval: Double) {
        triggerRunner.tune(stable: stable, interval: interval)
    }
    func triggerCaptureBegan() { triggerRunner.captureBegan() }
    func triggerCaptureFinished() {
        triggerRunner.captureFinished()
        restoreEVBiasIfNeeded()
    }

    private var evBiased = false

    func setOneShotEVBias(_ ev: Double) {
        guard let device else { return }
        do {
            try device.lockForConfiguration()
            let v = max(device.minExposureTargetBias,
                        min(device.maxExposureTargetBias, Float(ev)))
            device.setExposureTargetBias(v)
            device.unlockForConfiguration()
            evBiased = true
        } catch {
            SessionLog.write("evbias skipped: \(error.localizedDescription)")
        }
    }

    private func restoreEVBiasIfNeeded() {
        guard evBiased, let device else { return }
        evBiased = false
        do {
            try device.lockForConfiguration()
            device.setExposureTargetBias(0)
            device.unlockForConfiguration()
        } catch {
            SessionLog.write("evbias restore failed: \(error.localizedDescription)")
        }
    }

    func start() async {
        guard await AVCaptureDevice.requestAccess(for: .video) else {
            status = "Camera access denied. Grant it in Settings › Hoardling"
            failure = status
            return
        }
        configure()
    }

    func stop() {
        stopTrigger()
        onFire = nil
        onPhase = nil
        onCue = nil
        onTriggerTrace = nil
        Task.detached { [session] in
            if session.isRunning { session.stopRunning() }
        }
    }

    private func configure() {
        session.beginConfiguration()
        session.sessionPreset = .inputPriority

        guard let device = AVCaptureDevice.default(
            lensType, for: .video, position: .back) else {
            session.commitConfiguration()
            status = "No wide camera on this device"
            failure = status
            return
        }
        self.device = device

        session.inputs.forEach { session.removeInput($0) }
        guard let input = try? AVCaptureDeviceInput(device: device),
              session.canAddInput(input) else {
            session.commitConfiguration()
            status = "Could not open the camera"
            failure = status
            return
        }
        session.addInput(input)

        if let best = bestPhotoFormat(device) {
            do {
                try device.lockForConfiguration()
                device.activeFormat = best
                device.unlockForConfiguration()
            } catch {
                SessionLog.write("camera format failed: \(error.localizedDescription)")
                status = "Could not set up the camera"
                failure = status
            }
        }
        let tap = CMVideoFormatDescriptionGetDimensions(device.activeFormat.formatDescription)
        SessionLog.write("tap \(tap.width)x\(tap.height)")

        autoAvailable = triggerRunner.attach(to: session, device: device)

        session.commitConfiguration()

        trackRotation(of: device)
        Task.detached { [session] in session.startRunning() }
        status = "Settling…"
        Task { await settleAndLock() }
        observeSessionLife()
    }

    private var lifeObserved = false

    private func observeSessionLife() {
        guard !lifeObserved else { return }
        lifeObserved = true
        let nc = NotificationCenter.default
        nc.addObserver(forName: AVCaptureSession.wasInterruptedNotification,
                       object: session, queue: .main) { [weak self] _ in
            Task { @MainActor [weak self] in
                self?.status = "Camera interrupted — another app or a call has it"
            }
        }
        nc.addObserver(forName: AVCaptureSession.interruptionEndedNotification,
                       object: session, queue: .main) { [weak self] _ in
            Task { @MainActor [weak self] in self?.resumeAfterInterruption() }
        }
        nc.addObserver(forName: AVCaptureSession.runtimeErrorNotification,
                       object: session, queue: .main) { [weak self] _ in
            Task { @MainActor [weak self] in self?.resumeAfterInterruption() }
        }
    }

    private func resumeAfterInterruption() {
        status = "Resuming camera…"
        Task.detached { [session] in
            if !session.isRunning { session.startRunning() }
        }
        status = "Ready"
    }

    private func trackRotation(of device: AVCaptureDevice) {
        let coordinator = AVCaptureDevice.RotationCoordinator(device: device, previewLayer: nil)
        rotationCoordinator = coordinator
        applyRotation()
        rotationObservation = coordinator.observe(
            \.videoRotationAngleForHorizonLevelPreview, options: [.new]
        ) { [weak self] _, _ in
            Task { @MainActor in self?.applyRotation() }
        }
    }

    private func applyRotation() {
        guard let coordinator = rotationCoordinator else { return }
        let angle = coordinator.videoRotationAngleForHorizonLevelPreview
        previewRotation = angle
        if let conn = previewConnection, conn.isVideoRotationAngleSupported(angle) {
            conn.videoRotationAngle = angle
        }
    }

    var previewConnection: AVCaptureConnection? {
        didSet { applyRotation() }
    }

    func settleAndLock() async {
        guard let device else { return }
        do {
            try device.lockForConfiguration()
            if device.isFocusModeSupported(.continuousAutoFocus) {
                device.focusMode = .continuousAutoFocus
            }
            if device.isExposureModeSupported(.continuousAutoExposure) {
                device.exposureMode = .continuousAutoExposure
            }
            if device.isWhiteBalanceModeSupported(.continuousAutoWhiteBalance) {
                device.whiteBalanceMode = .continuousAutoWhiteBalance
            }
            let centre = CGPoint(x: 0.5, y: 0.5)
            if device.isFocusPointOfInterestSupported { device.focusPointOfInterest = centre }
            if device.isExposurePointOfInterestSupported {
                device.exposurePointOfInterest = centre
            }
            if device.isAutoFocusRangeRestrictionSupported {
                device.autoFocusRangeRestriction = .near
            }
            device.unlockForConfiguration()
        } catch {
            SessionLog.write("camera configure failed: \(error.localizedDescription)")
            status = "Could not set up the camera"
            failure = status
            return
        }

        try? await Task.sleep(for: .milliseconds(1200))
        lockMetering()
        status = "Waiting for the first card"
    }

    private var emptyReads = 0

    func focus(afterGoodRead good: Bool) {
        guard device != nil else { return }
        if good {
            emptyReads = 0
            guard !locked else { return }
            lockFocus()
            return
        }
        guard locked else { return }
        emptyReads += 1
        guard emptyReads >= 2 else { return }
        emptyReads = 0
        thawFocus()
    }

    private func lockFocus() {
        guard let device, device.isFocusModeSupported(.locked) else { return }
        guard (try? device.lockForConfiguration()) != nil else {
            SessionLog.write("focus lock skipped: camera is busy")
            return
        }
        lensPosition = device.lensPosition
        device.focusMode = .locked
        device.unlockForConfiguration()
        locked = true
        SessionLog.write(String(format: "focus locked at %.3f", lensPosition))
        status = "Focused"
    }

    private func thawFocus() {
        guard let device, device.isFocusModeSupported(.continuousAutoFocus) else { return }
        guard (try? device.lockForConfiguration()) != nil else {
            SessionLog.write("focus thaw skipped: camera is busy")
            return
        }
        device.focusMode = .continuousAutoFocus
        device.unlockForConfiguration()
        locked = false
        status = "Refocusing"
    }

    private func lockMetering() {
        guard let device else { return }
        guard (try? device.lockForConfiguration()) != nil else {
            SessionLog.write("metering lock skipped: camera is busy")
            return
        }
        defer { device.unlockForConfiguration() }
        if device.isExposureModeSupported(.locked) { device.exposureMode = .locked }
        if device.isWhiteBalanceModeSupported(.locked) { device.whiteBalanceMode = .locked }
    }

    func unlock() {
        guard let device else { return }
        guard (try? device.lockForConfiguration()) != nil else {
            SessionLog.write("unlock skipped: camera is busy")
            return
        }
        defer { device.unlockForConfiguration() }
        if device.isFocusModeSupported(.continuousAutoFocus) {
            device.focusMode = .continuousAutoFocus
        }
        if device.isExposureModeSupported(.continuousAutoExposure) {
            device.exposureMode = .continuousAutoExposure
        }
        if device.isWhiteBalanceModeSupported(.continuousAutoWhiteBalance) {
            device.whiteBalanceMode = .continuousAutoWhiteBalance
        }
        locked = false
        status = "Auto"
    }

    func refocus(at point: CGPoint) {
        guard let device else { return }
        guard (try? device.lockForConfiguration()) != nil else {
            SessionLog.write("refocus skipped: camera is busy")
            return
        }
        defer { device.unlockForConfiguration() }
        if device.isFocusPointOfInterestSupported, device.isFocusModeSupported(.autoFocus) {
            device.focusPointOfInterest = point
            device.focusMode = .autoFocus
        }
        if device.isExposurePointOfInterestSupported,
           device.isExposureModeSupported(.autoExpose) {
            device.exposurePointOfInterest = point
            device.exposureMode = .autoExpose
        }
        locked = false
        emptyReads = 0
        status = "Refocusing…"
        Task { [weak self] in
            try? await Task.sleep(for: .milliseconds(900))
            guard let self else { return }
            self.lockFocus()
            self.lockMetering()
        }
    }

    func setLensPosition(_ p: Float) {
        guard let device, device.isFocusModeSupported(.locked),
              device.isLockingFocusWithCustomLensPositionSupported else { return }
        guard (try? device.lockForConfiguration()) != nil else {
            SessionLog.write("lens position skipped: camera is busy")
            return
        }
        device.setFocusModeLocked(lensPosition: p) { _ in }
        device.unlockForConfiguration()
        lensPosition = p
        SessionLog.write(String(format: "focus set to %.3f", p))
        status = "Focused"
    }

    func setTorch(_ level: Float) {
        guard let device, device.hasTorch else { return }
        guard (try? device.lockForConfiguration()) != nil else {
            SessionLog.write("torch skipped: camera is busy")
            return
        }
        defer { device.unlockForConfiguration() }
        if level <= 0.01 {
            device.torchMode = .off
            torchLevel = 0
        } else {
            do {
                try device.setTorchModeOn(
                    level: min(level, AVCaptureDevice.maxAvailableTorchLevel))
                torchLevel = level
            } catch {
                SessionLog.write("torch refused: \(error.localizedDescription)")
            }
        }
    }

    var hasTorch: Bool { device?.hasTorch ?? false }

    var previewAspect: CGFloat {
        guard let device else { return 3.0 / 4.0 }
        let d = CMVideoFormatDescriptionGetDimensions(device.activeFormat.formatDescription)
        guard d.width > 0, d.height > 0 else { return 3.0 / 4.0 }
        let sideways = Int(previewRotation) % 180 == 0
        return sideways
            ? CGFloat(d.height) / CGFloat(d.width)
            : CGFloat(d.width) / CGFloat(d.height)
    }
    var minFocusMM: Int { max(0, Int(device?.minimumFocusDistance ?? 0)) }

    func capture() async -> CapturedFrame? {
        guard !busy else { return nil }
        busy = true
        defer { busy = false }
        let frame = await captureFromVideo()
        lastCapture = frame
        return frame
    }
}

struct CapturedFrame {
    let image: CGImage
    let orientation: CGImagePropertyOrientation
}

func maxPhotoDimensions(_ format: AVCaptureDevice.Format) -> CMVideoDimensions? {
    format.supportedMaxPhotoDimensions
        .max { Int($0.width) * Int($0.height) < Int($1.width) * Int($1.height) }
}

func bestPhotoFormat(_ device: AVCaptureDevice) -> AVCaptureDevice.Format? {
    func pixels(_ f: AVCaptureDevice.Format) -> Int {
        guard let m = maxPhotoDimensions(f) else { return 0 }
        return Int(m.width) * Int(m.height)
    }
    let best = device.formats.map(pixels).max() ?? 0
    guard best > 0 else { return device.formats.last }
    return device.formats
        .filter { pixels($0) == best }
        .max { a, b in
            let ra = a.videoSupportedFrameRateRanges.first?.maxFrameRate ?? 0
            let rb = b.videoSupportedFrameRateRanges.first?.maxFrameRate ?? 0
            return ra < rb
        }
}

private let sharedContext = CIContext()
