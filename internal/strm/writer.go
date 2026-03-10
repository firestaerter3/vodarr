package strm

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/vodarr/vodarr/internal/probe"
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

// WriteResult holds paths to both files created for one media item.
type WriteResult struct {
	StrmPath string // .strm file containing the stream URL (for Plex/Emby)
	MkvPath  string // companion .mkv stub (for Sonarr/Radarr import filter)
}

// WriteMovie creates a .strm file and companion .mkv stub for a movie.
// Path: {output}/{movies}/{Movie Name (Year)}/{Movie.Name.Year.WEB-DL.strm}
// info is optional; if non-nil its metadata is embedded in the .mkv header.
func (w *Writer) WriteMovie(name, year, streamURL string, info *probe.MediaInfo) (WriteResult, error) {
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
	return w.write(dir, filename, streamURL, info)
}

// WriteEpisode creates a .strm file and companion .mkv stub for a single TV episode.
// Path: {output}/{tv}/{Series Name}/Season {N}/{Series.Name.S01E01.Title.WEB-DL.strm}
// info is optional; if non-nil its metadata is embedded in the .mkv header.
func (w *Writer) WriteEpisode(seriesName string, season, episode int, title, streamURL string, info *probe.MediaInfo) (WriteResult, error) {
	seasonDir := fmt.Sprintf("Season %02d", season)
	dir := filepath.Join(w.outputPath, w.seriesDir, folderSafe(seriesName), seasonDir)

	epTag := fmt.Sprintf("S%02dE%02d", season, episode)
	filename := fileSafe(seriesName) + "." + epTag
	if title != "" {
		filename += "." + fileSafe(title)
	}
	filename += ".WEB-DL.strm"

	return w.write(dir, filename, streamURL, info)
}

func (w *Writer) write(dir, filename, content string, info *probe.MediaInfo) (WriteResult, error) {
	// 2B: Verify the resolved path is still under the output directory
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return WriteResult{}, fmt.Errorf("abs path: %w", err)
	}
	absOutput, err := filepath.Abs(w.outputPath)
	if err != nil {
		return WriteResult{}, fmt.Errorf("abs output path: %w", err)
	}
	if !strings.HasPrefix(absDir+string(filepath.Separator), absOutput+string(filepath.Separator)) {
		return WriteResult{}, fmt.Errorf("path traversal detected: %s escapes output directory", dir)
	}

	if err := os.MkdirAll(absDir, 0755); err != nil {
		return WriteResult{}, fmt.Errorf("mkdir %s: %w", absDir, err)
	}

	strmPath := filepath.Join(absDir, filename)
	if err := os.WriteFile(strmPath, []byte(content+"\n"), 0644); err != nil {
		return WriteResult{}, fmt.Errorf("write strm %s: %w", strmPath, err)
	}

	// Companion .mkv stub: valid EBML/Matroska header followed by sparse padding to
	// 500 MB logical size so Sonarr/Radarr's sample-detection threshold is satisfied.
	// Physical disk usage is ~0 bytes on ext4/xfs (sparse file).
	// When info is non-nil, the header encodes real codec/resolution/duration so that
	// ffprobe and arr applications see accurate media info without the ffprobe wrapper.
	mkvPath := strings.TrimSuffix(strmPath, ".strm") + ".mkv"
	mkvFile, err := os.OpenFile(mkvPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return WriteResult{}, fmt.Errorf("create mkv stub %s: %w", mkvPath, err)
	}
	if _, err := mkvFile.Write(BuildMKVHeader(info)); err != nil {
		mkvFile.Close()
		return WriteResult{}, fmt.Errorf("write mkv header %s: %w", mkvPath, err)
	}
	stubSize := int64(500 * 1024 * 1024)
	if info != nil && info.Size > 1024*1024 {
		stubSize = info.Size
	}
	if err := mkvFile.Truncate(stubSize); err != nil {
		mkvFile.Close()
		return WriteResult{}, fmt.Errorf("truncate mkv stub %s: %w", mkvPath, err)
	}
	if err := mkvFile.Close(); err != nil {
		return WriteResult{}, fmt.Errorf("close mkv stub %s: %w", mkvPath, err)
	}

	return WriteResult{StrmPath: strmPath, MkvPath: mkvPath}, nil
}

// RemoveMovie deletes the directory for a movie (including all .strm/.mkv files).
// The directory is determined by the same naming rules as WriteMovie.
// Returns nil if the directory does not exist.
func (w *Writer) RemoveMovie(name, year string) error {
	folderName := folderSafe(name)
	if year != "" {
		folderName = fmt.Sprintf("%s (%s)", folderName, year)
	}
	dir := filepath.Join(w.outputPath, w.moviesDir, folderName)
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("abs path: %w", err)
	}
	absOutput, err := filepath.Abs(w.outputPath)
	if err != nil {
		return fmt.Errorf("abs output path: %w", err)
	}
	if !strings.HasPrefix(absDir+string(filepath.Separator), absOutput+string(filepath.Separator)) {
		return fmt.Errorf("path traversal detected: %s escapes output directory", dir)
	}
	if err := os.RemoveAll(absDir); err != nil {
		return fmt.Errorf("remove movie dir %s: %w", absDir, err)
	}
	return nil
}

// RemoveSeries deletes the directory for a series (including all seasons, .strm/.mkv files).
// The directory is determined by the same naming rules as WriteEpisode.
// Returns nil if the directory does not exist.
func (w *Writer) RemoveSeries(name string) error {
	dir := filepath.Join(w.outputPath, w.seriesDir, folderSafe(name))
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("abs path: %w", err)
	}
	absOutput, err := filepath.Abs(w.outputPath)
	if err != nil {
		return fmt.Errorf("abs output path: %w", err)
	}
	if !strings.HasPrefix(absDir+string(filepath.Separator), absOutput+string(filepath.Separator)) {
		return fmt.Errorf("path traversal detected: %s escapes output directory", dir)
	}
	if err := os.RemoveAll(absDir); err != nil {
		return fmt.Errorf("remove series dir %s: %w", absDir, err)
	}
	return nil
}

// xtreamURLRe matches Xtream stream URLs of the form:
//
//	{scheme}://{host}/{movie|series}/{user}/{pass}/{id}.{ext}
//
// Capture groups: (1) scheme+host, (2) stream type, (3) stream ID, (4) extension.
var xtreamURLRe = regexp.MustCompile(`^https?://[^/]+/(movie|series)/[^/]+/[^/]+/(\d+)\.([^/\n]+)$`)

// RefreshURLs walks the output directory, finds every .strm file, and rewrites
// its URL using buildURL(streamType, streamID, ext). Files whose URL does not
// match the Xtream pattern are skipped. Returns the number of files rewritten
// and the first error encountered (processing continues past errors).
func (w *Writer) RefreshURLs(buildURL func(streamType, streamID, ext string) (string, error)) (int, error) {
	absOutput, err := filepath.Abs(w.outputPath)
	if err != nil {
		return 0, fmt.Errorf("abs output path: %w", err)
	}

	count := 0
	var firstErr error

	_ = filepath.WalkDir(absOutput, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() || !strings.HasSuffix(path, ".strm") {
			return nil
		}

		// Path traversal guard
		absPath, absErr := filepath.Abs(path)
		if absErr != nil || !strings.HasPrefix(absPath+string(filepath.Separator), absOutput+string(filepath.Separator)) {
			slog.Warn("strm refresh: skipping file outside output dir", "path", path)
			return nil
		}

		data, readErr := os.ReadFile(absPath)
		if readErr != nil {
			if firstErr == nil {
				firstErr = readErr
			}
			slog.Warn("strm refresh: read failed", "path", absPath, "error", readErr)
			return nil
		}

		rawURL := strings.TrimSpace(string(data))
		m := xtreamURLRe.FindStringSubmatch(rawURL)
		if m == nil {
			// Not an Xtream URL — skip silently
			return nil
		}

		streamType, streamIDStr, ext := m[1], m[2], m[3]

		newURL, buildErr := buildURL(streamType, streamIDStr, ext)
		if buildErr != nil {
			if firstErr == nil {
				firstErr = buildErr
			}
			slog.Warn("strm refresh: buildURL failed", "path", absPath, "error", buildErr)
			return nil
		}
		if newURL == "" || newURL == rawURL {
			return nil // unknown type or no change needed
		}

		if writeErr := os.WriteFile(absPath, []byte(newURL+"\n"), 0644); writeErr != nil {
			if firstErr == nil {
				firstErr = writeErr
			}
			slog.Warn("strm refresh: write failed", "path", absPath, "error", writeErr)
			return nil
		}
		count++
		return nil
	})

	return count, firstErr
}

var illegalChars = regexp.MustCompile(`[<>:"/\\|?*]`)

const maxNameLen = 200

// folderSafe returns a filesystem-safe folder name (spaces preserved).
func folderSafe(s string) string {
	s = stripControlChars(s)
	s = illegalChars.ReplaceAllString(s, "")
	// 2B: Remove path traversal sequences
	for strings.Contains(s, "..") {
		s = strings.ReplaceAll(s, "..", ".")
	}
	s = strings.TrimSpace(s)
	if len(s) > maxNameLen {
		s = s[:maxNameLen]
	}
	return s
}

// stripControlChars removes ASCII control characters (< 0x20), null bytes,
// Unicode control chars (Cc), and Unicode format chars (Cf), which includes
// bidirectional override characters such as U+202E (RIGHT-TO-LEFT OVERRIDE).
func stripControlChars(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0 || unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
}

// fileSafe returns a dot-separated filename-safe string.
func fileSafe(s string) string {
	s = stripControlChars(s)
	s = illegalChars.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, " ", ".")
	for strings.Contains(s, "..") {
		s = strings.ReplaceAll(s, "..", ".")
	}
	s = strings.Trim(s, ".")
	return s
}
