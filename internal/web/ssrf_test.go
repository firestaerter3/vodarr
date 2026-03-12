package web

import "testing"

func TestValidateURLScheme(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"valid http", "http://127.0.0.1:9090/api", false},    // private IPs allowed
		{"valid https", "https://192.168.1.1:8989/api", false}, // private IPs allowed
		{"file scheme", "file:///etc/passwd", true},
		{"ftp scheme", "ftp://example.com/file", true},
		{"gopher scheme", "gopher://example.com/", true},
		{"empty url", "", true},
		{"no host", "http:///path", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateURLScheme(tc.url)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateURLScheme(%q) error = %v, wantErr %v", tc.url, err, tc.wantErr)
			}
		})
	}
}

