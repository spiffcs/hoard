package release

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spiffcs/hoard/internal/buildinfo"
)

var releasesBaseURL = "https://github.com/spiffcs/hoard"

// Latest reports the tag of the newest published release. It reads the tag out
// of the redirect that /releases/latest serves rather than calling the API, so
// it needs no token and is not subject to the unauthenticated rate limit. The
// redirect is deliberately not followed: the Location header is the whole
// answer, and following it would download a release page for nothing.
func Latest(ctx context.Context, timeout time.Duration) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesBaseURL+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", buildinfo.UserAgent)

	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("asking for the latest release: %w", err)
	}
	defer resp.Body.Close()

	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", fmt.Errorf("no Location header in the reply from %s", req.URL)
	}
	tag, ok := tagFromLocation(loc)
	if !ok {
		return "", fmt.Errorf("no release tag in %q", loc)
	}
	return tag, nil
}

func tagFromLocation(loc string) (string, bool) {
	const marker = "/tag/"
	i := strings.LastIndex(loc, marker)
	if i < 0 {
		return "", false
	}
	if tag := strings.Trim(loc[i+len(marker):], "/"); tag != "" {
		return tag, true
	}
	return "", false
}

func ReleaseURL(tag string) string { return releasesBaseURL + "/releases/tag/" + tag }
