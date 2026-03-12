package web

import (
	"fmt"
	"net/url"
)

// validateURLScheme checks that rawURL has an http or https scheme and is parseable.
// Use for authenticated endpoints where private IPs are legitimate (e.g. local arr instances).
func validateURLScheme(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL scheme %q not allowed (must be http or https)", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("URL has no host")
	}
	return nil
}

