package strm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteMovie(t *testing.T) {
	dir := t.TempDir()
	w := NewWriter(dir, "movies", "tv")

	path, err := w.WriteMovie("Inception", "2010", "http://server/movie/user/pass/123.mkv")
	if err != nil {
		t.Fatalf("WriteMovie: %v", err)
	}

	// Check path structure
	rel, _ := filepath.Rel(dir, path)
	if !strings.HasPrefix(rel, "movies/") {
		t.Errorf("path %q should be under movies/", rel)
	}
	if !strings.Contains(rel, "Inception (2010)") {
		t.Errorf("path %q should contain folder 'Inception (2010)'", rel)
	}
	if !strings.HasSuffix(path, ".strm") {
		t.Errorf("path %q should end with .strm", path)
	}

	// Check file content
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.TrimSpace(string(content)) != "http://server/movie/user/pass/123.mkv" {
		t.Errorf("content = %q, want stream URL", string(content))
	}
}

func TestWriteMovieNoYear(t *testing.T) {
	dir := t.TempDir()
	w := NewWriter(dir, "movies", "tv")

	path, err := w.WriteMovie("Unknown Movie", "", "http://x/1.mkv")
	if err != nil {
		t.Fatalf("WriteMovie: %v", err)
	}
	if strings.Contains(path, "()") {
		t.Errorf("path %q should not contain empty year parens", path)
	}
}

func TestWriteEpisode(t *testing.T) {
	dir := t.TempDir()
	w := NewWriter(dir, "movies", "tv")

	path, err := w.WriteEpisode("Breaking Bad", 3, 10, "Fly", "http://server/series/user/pass/456.mkv")
	if err != nil {
		t.Fatalf("WriteEpisode: %v", err)
	}

	rel, _ := filepath.Rel(dir, path)

	if !strings.HasPrefix(rel, "tv/") {
		t.Errorf("path %q should be under tv/", rel)
	}
	if !strings.Contains(rel, "Breaking Bad") {
		t.Errorf("path %q should contain series name", rel)
	}
	if !strings.Contains(rel, "Season 03") {
		t.Errorf("path %q should contain Season 03", rel)
	}
	if !strings.Contains(rel, "S03E10") {
		t.Errorf("path %q should contain S03E10, got %q", rel, rel)
	}
	if !strings.HasSuffix(path, ".strm") {
		t.Errorf("path %q should end with .strm", path)
	}
}

func TestFileSafe(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Blade Runner 2049", "Blade.Runner.2049"},
		{"S.W.A.T.", "S.W.A.T"},
		{"Movie: Subtitle", "Movie.Subtitle"}, // colon stripped, space → dot
	}
	for _, c := range cases {
		got := fileSafe(c.in)
		if got != c.want {
			t.Errorf("fileSafe(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
