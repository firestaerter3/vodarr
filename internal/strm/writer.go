package strm

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Writer creates .strm files in the configured output directory.
type Writer struct {
	outputPath string
	moviesDir  string
	seriesDir  string
}

func NewWriter(outputPath, moviesDir, seriesDir string) *Writer {
	return &Writer{
		outputPath: outputPath,
		moviesDir:  moviesDir,
		seriesDir:  seriesDir,
	}
}

// WriteMovie creates a .strm file for a movie.
// Path: {output}/{movies}/{Movie Name (Year)}/{Movie.Name.Year.WEB-DL.strm}
func (w *Writer) WriteMovie(name, year, streamURL string) (string, error) {
	folderName := folderSafe(name)
	if year != "" {
		folderName = fmt.Sprintf("%s (%s)", folderName, year)
	}

	filename := fileSafe(name)
	if year != "" {
		filename = fmt.Sprintf("%s.%s.WEB-DL.strm", filename, year)
	} else {
		filename = fmt.Sprintf("%s.WEB-DL.strm", filename)
	}

	dir := filepath.Join(w.outputPath, w.moviesDir, folderName)
	return w.write(dir, filename, streamURL)
}

// WriteEpisode creates a .strm file for a single TV episode.
// Path: {output}/{tv}/{Series Name}/Season {N}/{Series.Name.S01E01.Title.WEB-DL.strm}
func (w *Writer) WriteEpisode(seriesName string, season, episode int, title, streamURL string) (string, error) {
	seasonDir := fmt.Sprintf("Season %02d", season)
	dir := filepath.Join(w.outputPath, w.seriesDir, folderSafe(seriesName), seasonDir)

	epTag := fmt.Sprintf("S%02dE%02d", season, episode)
	filename := fileSafe(seriesName) + "." + epTag
	if title != "" {
		filename += "." + fileSafe(title)
	}
	filename += ".WEB-DL.strm"

	return w.write(dir, filename, streamURL)
}

func (w *Writer) write(dir, filename, content string) (string, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}

	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte(content+"\n"), 0644); err != nil {
		return "", fmt.Errorf("write strm %s: %w", path, err)
	}
	return path, nil
}

var illegalChars = regexp.MustCompile(`[<>:"/\\|?*]`)

// folderSafe returns a filesystem-safe folder name (spaces preserved).
func folderSafe(s string) string {
	s = illegalChars.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)
	return s
}

// fileSafe returns a dot-separated filename-safe string.
func fileSafe(s string) string {
	s = illegalChars.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, " ", ".")
	s = strings.ReplaceAll(s, "..", ".")
	s = strings.Trim(s, ".")
	return s
}
