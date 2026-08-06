.PHONY: build scan scan-check scan-test cardkit cardkit-score scan-ios scan-ios-install scan-ios-test \
        test vet all clean generate-json-schema

# Build the hoard binary.
build:
	go build -o hoard .

# Build the macOS scan helper (bin/hoard-scan.app). macOS + Xcode only.
#
# The helper owns no camera: it is the Mac end of the link to the iPhone app,
# which is what actually captures and reads. See docs/ios-development.md for
# building that side.
scan:
	./build-scan.sh

# Replay the checked-in scan fixtures through the reader and diff the extracted
# card lists against their goldens (macOS only). The reader is cardkit-probe,
# so this needs `make cardkit` rather than `make scan`.
scan-check: cardkit
	./scan/fixtures/sweep.sh

# Unit-test the Swift side's pure logic — the trigger state machine, the text
# heuristics, the border reader's arithmetic, the wire contract. Complements
# scan-check rather than replacing it: the sweep proves what the reader gets
# off real frames, these prove the rules in isolation and run in milliseconds.
scan-test:
	swift test --package-path scan/hoard-scan

# Build the iPhone head's read pipeline as a macOS binary, so it can be run over
# an image file: it scores scan/corpus's labelled images and replays
# scan/fixtures' frames against their goldens.
cardkit:
	swift build -c release --package-path scan/hoard-scan --product cardkit-probe
	@mkdir -p bin
	@cp scan/hoard-scan/.build/release/cardkit-probe bin/cardkit-probe
	@echo "Built bin/cardkit-probe"

# Score the corpus in one process: ~23s.
#
# There used to be a scan/corpus/sweep.sh beside this that scored the macOS
# helper's own reader by launching one process per image. That reader belonged
# to the Continuity Camera path and went with it; the script went too, and this
# is now the only corpus scorer. Launching 231 processes rather than reading 231
# cards had made the one loop guarding against accuracy regressions the slowest
# loop in the project — slow enough to be skipped, which is the same as not
# having it.
#
#   make cardkit-score                 # the table
#   make cardkit-score ARGS=--misses   # and every card that failed
cardkit-score: cardkit
	@./bin/cardkit-probe --score scan/corpus/manifest.tsv $(ARGS)

# Build the iPhone capture head. Needs xcodegen, the iOS platform payload
# (xcodebuild -downloadPlatform iOS) and a signing team — build-scan-ios.sh
# reports whichever is missing. See docs/ios-development.md.
scan-ios:
	./build-scan-ios.sh

# Same, then install onto an attached, unlocked iPhone with Developer Mode on.
scan-ios-install:
	./build-scan-ios.sh --install

# Run ScanKit's unit tests on the iOS simulator. They are Vision-free and
# CoreGraphics-only, so they prove Core/ still compiles and behaves for iOS
# without needing a device or a signing identity. This is the cheap gate; the
# expensive one is the on-device fixture replay in the app itself, because
# simulator Vision is not device Vision and goldens must never come from it.
#
# hoard-scan-Package is SwiftPM's generated all-targets scheme — the ScanKit
# scheme is the library alone and carries no test action. The simulator is
# discovered rather than named, because the installed runtime's device list
# changes with every Xcode update and a hardcoded model goes stale silently.
SIM_NAME := $(shell xcrun simctl list devices available 2>/dev/null | \
	grep -m1 -o 'iPhone [A-Za-z0-9 ]*' | sed 's/ *$$//')
scan-ios-test:
	cd scan/hoard-scan && xcodebuild test -scheme hoard-scan-Package \
		-destination 'platform=iOS Simulator,name=$(SIM_NAME)' CODE_SIGNING_ALLOWED=NO

# Build everything needed for the full experience (binary + scan helper).
all: build scan

# Regenerate schema/json/ from the internal/hoardjson model. If the current
# schema version has been released, bump hoardjson.SchemaVersion first.
generate-json-schema:
	go run ./schema/json/generate

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -rf hoard bin
