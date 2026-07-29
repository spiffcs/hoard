.PHONY: build scan test vet all clean

# Build the hoard binary.
build:
	go build -o hoard .

# Build the macOS camera-scan helper (bin/hoard-scan.app). macOS + Xcode only.
scan:
	./build-scan.sh

# Build everything needed for the full experience (binary + scan helper).
all: build scan

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -rf hoard bin
