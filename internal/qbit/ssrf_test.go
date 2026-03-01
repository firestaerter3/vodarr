package qbit

import "testing"

func TestValidateDescriptorURL(t *testing.T) {
	cases := []struct {
		url     string
		wantErr bool
	}{
		{"http://localhost:7878/api?t=get&id=1", false},
		{"https://vodarr:7878/api?t=get&id=1", false},
		{"file:///etc/passwd", true},
		{"ftp://host/something", true},
		{"http://internal/api?t=movie", true},  // t != get
		{"http://internal/api?t=search", true}, // t != get
		{"", true},
		{"not-a-url", true}, // no scheme
	}

	for _, c := range cases {
		err := validateDescriptorURL(c.url)
		if (err != nil) != c.wantErr {
			if c.wantErr {
				t.Errorf("validateDescriptorURL(%q): expected error, got nil", c.url)
			} else {
				t.Errorf("validateDescriptorURL(%q): unexpected error: %v", c.url, err)
			}
		}
	}
}
