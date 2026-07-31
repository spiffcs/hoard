.PHONY: build scan test vet all clean generate-json-schema

# Build the hoard binary.
build:
	go build -o hoard .

# Build the macOS camera-scan helper (bin/hoard-scan.app). macOS + Xcode only.
scan:
	./build-scan.sh

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
