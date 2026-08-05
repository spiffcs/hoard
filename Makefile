.PHONY: build scan scan-check scan-test cardkit cardkit-score scan-ios scan-ios-install scan-ios-test \
        test vet all clean generate-json-schema

# Build the hoard binary.
build:
	go build -o hoard .

# Build the macOS camera-scan helper (bin/hoard-scan.app). macOS + Xcode only.
scan:
	./build-scan.sh

# Replay the checked-in scan fixtures through the built helper and diff the
# extracted card lists against their goldens (macOS only; needs `make scan`).
scan-check:
	./scan/fixtures/sweep.sh

# Unit-test the helper's pure logic — the trigger state machine, the text
# heuristics, the border reader's arithmetic. Complements scan-check rather
# than replacing it: the sweep proves what the helper reads off real frames,
# these prove the rules in isolation and run in milliseconds.
scan-test:
	swift test --package-path scan/hoard-scan

# Build the iPhone capture head. Needs xcodegen, the iOS platform payload
# (xcodebuild -downloadPlatform iOS) and a signing team — build-scan-ios.sh
# reports whichever is missing.
# Build the iPhone head's read pipeline as a macOS binary, so it can be scored
# against scan/corpus's 231 labelled images with the harness the old pipeline is
# measured by:
#
#   make cardkit-score
#
# A rewrite is only defensible if it can be compared to what it replaces, on
# ground truth, in the same table.
cardkit:
	swift build -c release --package-path scan/hoard-scan --product cardkit-probe
	@mkdir -p bin
	@cp scan/hoard-scan/.build/release/cardkit-probe bin/cardkit-probe
	@echo "Built bin/cardkit-probe"

# Score the corpus in one process: ~23s, against sweep.sh's several minutes.
#
# sweep.sh spends its time launching 231 processes rather than reading 231
# cards, which made the one loop guarding against accuracy regressions the
# slowest loop in the project — slow enough to be skipped, which is the same as
# not having it. It stays as the cross-check for the *macOS* helper, which is a
# separate binary and cannot be scored in-process from here.
#
#   make cardkit-score            # the table
#   make cardkit-score ARGS=--misses   # and every card that failed
cardkit-score: cardkit
	@./bin/cardkit-probe --score scan/corpus/manifest.tsv $(ARGS)

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
