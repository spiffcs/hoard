#!/usr/bin/env bash
set -euo pipefail

# Runs the test suite with every outbound request pointed at a local blackhole,
# then fails if anything tried to leave the machine. A test that needs a remote
# response must serve it from an httptest server instead.

tmp="$(mktemp -d)"
trap 'kill "${proxy_pid:-}" 2>/dev/null || true; rm -rf "$tmp"' EXIT

cat > "$tmp/blackhole.go" <<'GO'
package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
)

func main() {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	fmt.Println(ln.Addr().String())
	os.Stdout.Sync()
	http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(os.Stderr, r.Method, r.Host, r.URL.String())
		http.Error(w, "the test suite may not use the network", http.StatusTeapot)
	}))
}
GO

( cd "$tmp" && go mod init blackhole >/dev/null 2>&1 )
( cd "$tmp" && go run blackhole.go ) > "$tmp/addr" 2> "$tmp/hits" &
proxy_pid=$!

for _ in $(seq 1 50); do [ -s "$tmp/addr" ] && break; sleep 0.1; done
addr="$(cat "$tmp/addr")"

status=0
env http_proxy="http://$addr" https_proxy="http://$addr" \
    HTTP_PROXY="http://$addr" HTTPS_PROXY="http://$addr" \
    ALL_PROXY="http://$addr" all_proxy="http://$addr" \
    NO_PROXY="" no_proxy="" \
    go test "$@" ./... || status=$?

if [ -s "$tmp/hits" ]; then
	echo
	echo "FAIL: the suite reached for the network:"
	sort -u "$tmp/hits" | sed 's/^/  /'
	exit 1
fi

if [ "$status" -ne 0 ]; then
	exit "$status"
fi
echo
echo "OK: suite passed and made no outbound requests."
