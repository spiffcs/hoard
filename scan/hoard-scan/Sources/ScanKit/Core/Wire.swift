// The wire contract now lives in the ScanWire target.
//
// Event, CardEntry, CollectorRead, HUDCommand, Device, DeviceList, emit and
// fail moved there so the Go side's protocol has one definition rather than
// one per pipeline — ScanKit and CardKit share no other code, and two structs
// describing the same JSON is a contract nobody is checking.
//
// What stays on this side is the mapping from ScanKit's own read types into
// those shapes; see Event.scan in ScanFrame.swift.

@_exported import ScanWire
