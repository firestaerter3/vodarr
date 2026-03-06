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

	result, err := w.WriteMovie("Inception", "2010", "http://server/movie/user/pass/123.mkv")
	if err != nil {
		t.Fatalf("WriteMovie: %v", err)
	}

	// Check .strm path structure
	rel, _ := filepath.Rel(dir, result.StrmPath)
	if !strings.HasPrefix(rel, "movies/") {
		t.Errorf("strm path %q should be under movies/", rel)
	}
	if !strings.Contains(rel, "Inception (2010)") {
		t.Errorf("strm path %q should contain folder 'Inception (2010)'", rel)
	}
	if !strings.HasSuffix(result.StrmPath, ".strm") {
		t.Errorf("strm path %q should end with .strm", result.StrmPath)
	}

	// Check file content
	content, err := os.ReadFile(result.StrmPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.TrimSpace(string(content)) != "http://server/movie/user/pass/123.mkv" {
		t.Errorf("content = %q, want stream URL", string(content))
	}

	// Check companion .mkv stub
	if !strings.HasSuffix(result.MkvPath, ".mkv") {
		t.Errorf("mkv path %q should end with .mkv", result.MkvPath)
	}
	mkvBase := strings.TrimSuffix(result.MkvPath, ".mkv")
	strmBase := strings.TrimSuffix(result.StrmPath, ".strm")
	if mkvBase != strmBase {
		t.Errorf("mkv and strm share basename: mkv=%q strm=%q", result.MkvPath, result.StrmPath)
	}
	if _, err := os.Stat(result.MkvPath); err != nil {
		t.Errorf("companion .mkv does not exist: %v", err)
	}
	mkvContent, _ := os.ReadFile(result.MkvPath)
	if len(mkvContent) != 0 {
		t.Errorf("companion .mkv should be empty, got %d bytes", len(mkvContent))
	}
}

func TestWriteMovieNoYear(t *testing.T) {
	dir := t.TempDir()
	w := NewWriter(dir, "movies", "tv")

	result, err := w.WriteMovie("Unknown Movie", "", "http://x/1.mkv")
	if err != nil {
		t.Fatalf("WriteMovie: %v", err)
	}
	if strings.Contains(result.StrmPath, "()") {
		t.Errorf("path %q should not contain empty year parens", result.StrmPath)
	}
}

func TestWriteEpisode(t *testing.T) {
	dir := t.TempDir()
	w := NewWriter(dir, "movies", "tv")

	result, err := w.WriteEpisode("Breaking Bad", 3, 10, "Fly", "http://server/series/user/pass/456.mkv")
	if err != nil {
		t.Fatalf("WriteEpisode: %v", err)
	}

	rel, _ := filepath.Rel(dir, result.StrmPath)

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
	if !strings.HasSuffix(result.StrmPath, ".strm") {
		t.Errorf("path %q should end with .strm", result.StrmPath)
	}
	// Companion .mkv must also exist alongside the .strm
	if _, err := os.Stat(result.MkvPath); err != nil {
		t.Errorf("companion .mkv does not exist: %v", err)
	}
}

func TestFileSafe(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Blade Runner 2049", "Blade.Runner.2049"},
		{"S.W.A.T.", "S.W.A.T"},
		{"Movie: Subtitle", "Movie.Subtitle"}, // colon stripped, space → dot
		// 4B: double-dot edge cases
		{"A..B", "A.B"},
		{"A...B", "A.B"},
		{"..", ""},
		{"...foo...", "foo"},
	}
	for _, c := range cases {
		got := fileSafe(c.in)
		if got != c.want {
			t.Errorf("fileSafe(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPathTraversal(t *testing.T) {
	dir := t.TempDir()
	w := NewWriter(dir, "movies", "tv")

	// Names with ".." are sanitized by folderSafe — they succeed safely (not traversal)
	result, err := w.WriteMovie("../evil", "2024", "http://x/1.mkv")
	if err != nil {
		t.Errorf("WriteMovie with ../evil: unexpected error: %v", err)
	}
	// Verify the file is inside the output directory
	if !strings.HasPrefix(result.StrmPath, dir) {
		t.Errorf("WriteMovie path %q escapes output dir %q", result.StrmPath, dir)
	}

	// Defense-in-depth: write() with a raw path escaping output dir must fail
	_, err = w.write(dir+"/../escaped", "test.strm", "http://x/1.mkv")
	if err == nil {
		t.Error("write with path escaping output dir: expected error, got nil")
	}
}

func TestFolderSafe(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Normal Title", "Normal Title"},
		{"../hack", ".hack"},  // .. collapsed to .
		{"A/B", "AB"},         // slash stripped
		{"\x00null\x01ctrl", "nullctrl"}, // control chars stripped
	}
	for _, c := range cases {
		got := folderSafe(c.in)
		if got != c.want {
			t.Errorf("folderSafe(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFileSafeStripsRTLOverride(t *testing.T) {
	// U+202E RIGHT-TO-LEFT OVERRIDE is a format char (unicode.Cf)
	name := "Movie\u202EName"
	got := fileSafe(name)
	for _, r := range got {
		if r == '\u202E' {
			t.Errorf("fileSafe did not strip RTL override U+202E from %q, got %q", name, got)
		}
	}
}

func TestFileSafeStripsNullByte(t *testing.T) {
	name := "Movie\x00Name"
	got := fileSafe(name)
	for _, r := range got {
		if r == 0 {
			t.Errorf("fileSafe did not strip null byte from %q, got %q", name, got)
		}
	}
}

func TestFolderSafeStripsRTLOverride(t *testing.T) {
	name := "Movie\u202EName"
	got := folderSafe(name)
	for _, r := range got {
		if r == '\u202E' {
			t.Errorf("folderSafe did not strip RTL override from %q, got %q", name, got)
		}
	}
}

func TestStrmFilePermissions(t *testing.T) {
	dir := t.TempDir()
	w := NewWriter(dir, "movies", "tv")

	result, err := w.WriteMovie("PermTest", "2024", "http://x/1.mkv")
	if err != nil {
		t.Fatalf("WriteMovie: %v", err)
	}
	for _, path := range []string{result.StrmPath, result.MkvPath} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat %q: %v", path, err)
		}
		perm := info.Mode().Perm()
		if perm != 0644 {
			t.Errorf("file %q permissions = %04o, want 0644", path, perm)
		}
	}
}
