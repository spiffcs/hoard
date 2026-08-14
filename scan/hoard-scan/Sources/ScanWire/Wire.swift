import Foundation

public struct Event: Encodable {
    public var event: String
    public var name: String = ""
    public var candidates: [String] = []
    public var rotation: Int = 0
    public var message: String = ""
    public var device: String = ""
    public var appVersion: String? = nil
    public var collectorNumber: String = ""
    public var setCode: String = ""
    public var bottomLines: [String] = []
    public var cards: [CardEntry]? = nil
    public var confidence: Float? = nil
    public var bandAnchored: Bool? = nil
    public var fireReason: String? = nil
    public var holdDelta: Double? = nil
    public var faceDelta: Double? = nil
    public var auto: Bool? = nil
    public var features: [String]? = nil
    public var state: String? = nil
    public var collectorAlts: [CollectorRead]? = nil
    public var finishHint: String? = nil
    public var language: String? = nil

    public init(
        event: String,
        name: String = "",
        candidates: [String] = [],
        rotation: Int = 0,
        message: String = "",
        device: String = "",
        appVersion: String? = nil,
        collectorNumber: String = "",
        setCode: String = "",
        bottomLines: [String] = [],
        cards: [CardEntry]? = nil,
        confidence: Float? = nil,
        bandAnchored: Bool? = nil,
        auto: Bool? = nil,
        fireReason: String? = nil,
        holdDelta: Double? = nil,
        faceDelta: Double? = nil,
        features: [String]? = nil,
        state: String? = nil,
        collectorAlts: [CollectorRead]? = nil,
        finishHint: String? = nil,
        language: String? = nil
    ) {
        self.event = event
        self.name = name
        self.candidates = candidates
        self.rotation = rotation
        self.message = message
        self.device = device
        self.appVersion = appVersion
        self.collectorNumber = collectorNumber
        self.setCode = setCode
        self.bottomLines = bottomLines
        self.cards = cards
        self.confidence = confidence
        self.bandAnchored = bandAnchored
        self.auto = auto
        self.fireReason = fireReason
        self.holdDelta = holdDelta
        self.faceDelta = faceDelta
        self.features = features
        self.state = state
        self.collectorAlts = collectorAlts
        self.finishHint = finishHint
        self.language = language
    }
}

public struct CardEntry: Encodable {
    public var name: String = ""
    public var candidates: [String] = []
    public var collectorNumber: String = ""
    public var setCode: String = ""
    public var confidence: Float = 0
    public var source: String = ""
    public var collectorAlts: [CollectorRead]? = nil
    public var finishHint: String = ""
    public var language: String? = nil
    public var finishSource: String? = nil
    public var sparkleScore: Double? = nil
    public var sparkleOffsetU: Double? = nil
    public var sparkleOffsetV: Double? = nil
    public var sparkleContrast: Double? = nil
    public var sparkleChromaScore: Double? = nil
    public var sparkleChromaContrast: Double? = nil
    public var numberSource: String? = nil
    public var copyrightYear: Int? = nil
    public var borderColor: String? = nil
    public var borderSource: String? = nil
    public var frameStyle: String? = nil

    public init(
        name: String = "",
        candidates: [String] = [],
        collectorNumber: String = "",
        setCode: String = "",
        confidence: Float = 0,
        source: String = "",
        collectorAlts: [CollectorRead]? = nil,
        finishHint: String = "",
        language: String? = nil,
        finishSource: String? = nil,
        sparkleScore: Double? = nil,
        sparkleOffsetU: Double? = nil,
        sparkleOffsetV: Double? = nil,
        sparkleContrast: Double? = nil,
        sparkleChromaScore: Double? = nil,
        sparkleChromaContrast: Double? = nil,
        numberSource: String? = nil,
        copyrightYear: Int? = nil,
        borderColor: String? = nil,
        borderSource: String? = nil,
        frameStyle: String? = nil
    ) {
        self.name = name
        self.candidates = candidates
        self.collectorNumber = collectorNumber
        self.setCode = setCode
        self.confidence = confidence
        self.source = source
        self.collectorAlts = collectorAlts
        self.finishHint = finishHint
        self.language = language
        self.finishSource = finishSource
        self.sparkleScore = sparkleScore
        self.sparkleOffsetU = sparkleOffsetU
        self.sparkleOffsetV = sparkleOffsetV
        self.sparkleContrast = sparkleContrast
        self.sparkleChromaScore = sparkleChromaScore
        self.sparkleChromaContrast = sparkleChromaContrast
        self.numberSource = numberSource
        self.copyrightYear = copyrightYear
        self.borderColor = borderColor
        self.borderSource = borderSource
        self.frameStyle = frameStyle
    }
}

public struct CollectorRead: Encodable {
    public var number = ""
    public var set = ""
    public var finish = ""
    public var language = ""
    public var pair = false

    enum CodingKeys: String, CodingKey {
        case number, set, finish, language
    }

    public init(
        number: String = "", set: String = "", finish: String = "",
        language: String = "", pair: Bool = false
    ) {
        self.number = number
        self.set = set
        self.finish = finish
        self.language = language
        self.pair = pair
    }
}

public struct HUDCommand: Decodable {
    public var amount: Double?
    public var tier: String?
    public var total: Double?
    public var name: String?
    public var finish: String?
    public var note: String?
    public var promote: Bool?
}

public struct Device: Encodable {
    public var id: String
    public var name: String
    public var kind: String

    public init(id: String, name: String, kind: String) {
        self.id = id
        self.name = name
        self.kind = kind
    }
}

public struct DeviceList: Encodable {
    public var devices: [Device]

    public init(devices: [Device]) { self.devices = devices }
}

public func emit(_ out: some Encodable) {
    guard let data = try? JSONEncoder().encode(out) else { return }
    FileHandle.standardOutput.write(data)
    FileHandle.standardOutput.write(Data("\n".utf8))
}

public func fail(_ message: String, code: Int32 = 1) -> Never {
    FileHandle.standardError.write(Data((message + "\n").utf8))
    exit(code)
}
