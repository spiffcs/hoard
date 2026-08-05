//go:build darwin

package scan

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ListDevices asks the helper which cameras are attached, so the caller can offer
// a choice between (say) an iPhone and the built-in webcam.
func ListDevices(ctx context.Context) ([]Device, error) {
	out, err := runHelper(ctx, "--list-devices")
	if err != nil {
		return nil, err
	}
	return parseDevices(out)
}

// VerifyPairing checks that a code actually opens a link to a phone.
//
// Pairing without this means "six digits were written to a file": a mistyped
// code is accepted happily and fails much later, at the start of a scanning
// session, looking like a network fault rather than a typo. The helper connects,
// waits for the phone's ready event — which it only sends after the pairing
// check passes — and exits.
func VerifyPairing(ctx context.Context, deviceID, code string) error {
	args := []string{"--remote", "--verify", "--code", code}
	if deviceID != "" {
		args = append(args, "--device", deviceID)
	}
	_, err := runHelper(ctx, args...)
	return err
}

// runHelper executes the helper for a one-shot query and returns its stdout. The
// helper emits JSON even when it exits non-zero, so stdout wins over the exit
// status; stderr is only surfaced when there's no JSON at all — a hard failure
// like no camera, denied permission, or a crash.
func runHelper(ctx context.Context, args ...string) ([]byte, error) {
	path, err := helperPath()
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, path, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	if out := bytes.TrimSpace(stdout.Bytes()); len(out) > 0 {
		return out, nil
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if msg := strings.TrimSpace(stderr.String()); msg != "" {
		return nil, errors.New(msg)
	}
	if runErr != nil {
		return nil, runErr
	}
	return nil, errors.New("scan helper produced no output")
}

// helperPath locates the hoard-scan executable inside its .app bundle. It checks
// $HOARD_SCAN, then next to the hoard binary, then ./bin (for `go run .`).
func helperPath() (string, error) {
	const rel = "hoard-scan.app/Contents/MacOS/hoard-scan"

	var candidates []string
	if env := os.Getenv("HOARD_SCAN"); env != "" {
		if strings.HasSuffix(env, ".app") {
			candidates = append(candidates, filepath.Join(env, "Contents/MacOS/hoard-scan"))
		} else {
			candidates = append(candidates, env)
		}
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates, filepath.Join(dir, rel), filepath.Join(dir, "bin", rel))
	}
	candidates = append(candidates, filepath.Join("bin", rel))

	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c, nil
		}
	}
	return "", ErrHelperMissing
}
