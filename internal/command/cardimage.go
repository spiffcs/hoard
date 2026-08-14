package command

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/spiffcs/hoard/internal/buildinfo"
)

var imageHTTP = &http.Client{Timeout: 20 * time.Second}

func fetchCardImage(ctx context.Context, scryfallID, url string) (image.Image, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	dir = filepath.Join(dir, "hoard", "images")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, scryfallID+".img")

	if b, err := os.ReadFile(path); err == nil {
		if img, _, err := image.Decode(bytes.NewReader(b)); err == nil {
			return img, nil
		}

	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", buildinfo.UserAgent)
	req.Header.Set("Accept", "image/jpeg, image/png, image/*")
	resp, err := imageHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("image fetch: %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes))
	if err != nil {
		return nil, err
	}

	img, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	if tmp, err := os.CreateTemp(dir, "img-*"); err == nil {
		if _, err := tmp.Write(body); err == nil && tmp.Close() == nil {
			_ = os.Rename(tmp.Name(), path)
		} else {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}
	return img, nil
}

const maxImageBytes = 5 << 20
