.PHONY: build scan scan-check scan-test test vet all clean generate-json-schema

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
