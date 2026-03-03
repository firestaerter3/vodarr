package qbit

import "testing"

func TestValidateDescriptorURL(t *testing.T) {
	cases := []struct {
		url         string
		allowedHost string
		wantErr     bool
	}{
		{"http://localhost:7878/api?t=get&id=1", "localhost:7878", false},
		{"https://vodarr:9091/api?t=get&id=1", "vodarr:9091", false},
		// host mismatch
		{"http://evil.com/api?t=get&id=1", "localhost:7878", true},
		{"http://localhost:9999/api?t=get&id=1", "localhost:7878", true},
		// no host restriction configured
		{"http://localhost:7878/api?t=get&id=1", "", false},
		// scheme checks
		{"file:///etc/passwd", "localhost:7878", true},
		{"ftp://host/something", "localhost:7878", true},
		// t param checks
		{"http://localhost:7878/api?t=movie", "localhost:7878", true},
		{"http://localhost:7878/api?t=search", "localhost:7878", true},
		// malformed
		{"", "localhost:7878", true},
		{"not-a-url", "localhost:7878", true},
	}

	for _, c := range cases {
		h := &Handler{newznabHost: c.allowedHost}
		err := h.validateDescriptorURL(c.url)
		if (err != nil) != c.wantErr {
			if c.wantErr {
				t.Errorf("validateDescriptorURL(%q, host=%q): expected error, got nil", c.url, c.allowedHost)
			} else {
				t.Errorf("validateDescriptorURL(%q, host=%q): unexpected error: %v", c.url, c.allowedHost, err)
			}
		}
	}
}
