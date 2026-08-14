package command

import (
	"context"
	"path/filepath"
	"time"

	"github.com/spiffcs/hoard/internal/scan"
	"github.com/spiffcs/hoard/internal/tui"
)

type linkScanner struct{}

func client() (*scan.Client, error) {
	dir, err := dataDir()
	if err != nil {
		return nil, err
	}
	return scan.NewClient(filepath.Join(dir, "hoard")), nil
}

func (linkScanner) Devices(ctx context.Context) ([]scan.Device, error) {
	c, err := client()
	if err != nil {
		return nil, err
	}
	return c.Devices(ctx)
}

func (linkScanner) Pair(deviceID, code string) error {
	c, err := client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), pairTimeout)
	defer cancel()
	return c.Pair(ctx, deviceID, code)
}

const pairTimeout = 30 * time.Second

func (linkScanner) Open(ctx context.Context, deviceID string) (tui.ScanSession, error) {
	c, err := client()
	if err != nil {
		return nil, err
	}

	s, err := c.Open(ctx, scan.OpenOptions{DeviceID: deviceID})
	if err != nil {
		return nil, err
	}
	return s, nil
}
