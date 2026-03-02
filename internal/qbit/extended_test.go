package qbit

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vodarr/vodarr/internal/strm"
	"github.com/vodarr/vodarr/internal/xtream"
)

// newAuthedHandler builds a Handler with username/password auth.
func newAuthedHandler(savePath string) *Handler {
	store := NewStore()
	return NewHandler(store, nil, nil, savePath, "admin", "secret")
}

// loginAndGetSID performs POST /api/v2/auth/login and returns the SID cookie value.
func loginAndGetSID(t *testing.T, h *Handler) string {
	t.Helper()
	form := url.Values{"username": {"admin"}, "password": {"secret"}}
	req := httptest.NewRequest("POST", "/api/v2/auth/login",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("login: status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Ok.") {
		t.Fatalf("login: body = %q, want 'Ok.'", body)
	}

	for _, c := range w.Result().Cookies() {
		if c.Name == "SID" {
			return c.Value
		}
	}
	t.Fatal("login response missing SID cookie")
	return ""
}

// addSID adds the SID cookie to the request.
func addSID(req *http.Request, sid string) {
	req.AddCookie(&http.Cookie{Name: "SID", Value: sid})
}

// ---- Login ----

func TestLoginSuccess(t *testing.T) {
	h := newAuthedHandler("/tmp")
	loginAndGetSID(t, h) // panics via t.Fatal if login fails
}

func TestLoginWrongPassword(t *testing.T) {
	h := newAuthedHandler("/tmp")
	form := url.Values{"username": {"admin"}, "password": {"wrong"}}
	req := httptest.NewRequest("POST", "/api/v2/auth/login",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("login: status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Fails.") {
		t.Errorf("wrong-password login should return 'Fails.', got %q", w.Body.String())
	}
}

// ---- Auth middleware ----

func TestAuthRequiredWithoutSID(t *testing.T) {
	h := newAuthedHandler("/tmp")
	req := httptest.NewRequest("GET", "/api/v2/torrents/info", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("without SID: status = %d, want 403", w.Code)
	}
}

func TestAuthPassesWithValidSID(t *testing.T) {
	h := newAuthedHandler("/tmp")
	sid := loginAndGetSID(t, h)

	req := httptest.NewRequest("GET", "/api/v2/torrents/info", nil)
	addSID(req, sid)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("with valid SID: status = %d, want 200", w.Code)
	}
}

func TestAuthFailsWithInvalidSID(t *testing.T) {
	h := newAuthedHandler("/tmp")
	req := httptest.NewRequest("GET", "/api/v2/torrents/info", nil)
	addSID(req, "invalidsid")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("invalid SID: status = %d, want 403", w.Code)
	}
}

// ---- Torrents delete ----

func TestTorrentsDelete(t *testing.T) {
	h := makeQbitHandler("/data/strm")
	h.store.Add(&Torrent{Hash: "todel", Name: "To Delete", SavePath: "/data/strm", State: StatePausedUP})

	// Verify it's there
	if h.store.Get("todel") == nil {
		t.Fatal("setup: torrent not found before delete")
	}

	form := url.Values{"hashes": {"todel"}}
	req := httptest.NewRequest("POST", "/api/v2/torrents/delete",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("delete: status = %d, want 200", w.Code)
	}
	if h.store.Get("todel") != nil {
		t.Error("torrent still present after delete")
	}
}

func TestTorrentsDeleteMultiple(t *testing.T) {
	h := makeQbitHandler("/data/strm")
	h.store.Add(&Torrent{Hash: "a1", Name: "A", SavePath: "/data/strm", State: StatePausedUP})
	h.store.Add(&Torrent{Hash: "b2", Name: "B", SavePath: "/data/strm", State: StatePausedUP})

	form := url.Values{"hashes": {"a1|b2"}}
	req := httptest.NewRequest("POST", "/api/v2/torrents/delete",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("delete: status = %d, want 200", w.Code)
	}
	if h.store.Get("a1") != nil || h.store.Get("b2") != nil {
		t.Error("not all torrents deleted")
	}
}

// ---- Category management ----

func TestCreateAndListCategory(t *testing.T) {
	h := makeQbitHandler("/data/strm")

	// Create
	form := url.Values{"category": {"tv-vodarr"}, "savePath": {"/data/strm/tv"}}
	req := httptest.NewRequest("POST", "/api/v2/torrents/createCategory",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("createCategory: status = %d, want 200", w.Code)
	}

	// List
	req2 := httptest.NewRequest("GET", "/api/v2/torrents/categories", nil)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("categories: status = %d, want 200", w2.Code)
	}

	var cats map[string]struct{ Name, SavePath string }
	if err := json.Unmarshal(w2.Body.Bytes(), &cats); err != nil {
		t.Fatalf("unmarshal categories: %v", err)
	}
	cat, ok := cats["tv-vodarr"]
	if !ok {
		t.Fatal("created category 'tv-vodarr' not found in listing")
	}
	if cat.SavePath != "/data/strm/tv" {
		t.Errorf("savePath = %q, want /data/strm/tv", cat.SavePath)
	}
}

// ---- App endpoints ----

func TestAppVersion(t *testing.T) {
	h := makeQbitHandler("/tmp")
	req := httptest.NewRequest("GET", "/api/v2/app/version", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.HasPrefix(w.Body.String(), "v") {
		t.Errorf("version should start with 'v': %q", w.Body.String())
	}
}

func TestWebapiVersion(t *testing.T) {
	h := makeQbitHandler("/tmp")
	req := httptest.NewRequest("GET", "/api/v2/app/webapiVersion", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if body == "" {
		t.Error("webapiVersion returned empty body")
	}
}

// ---- SSRF protection ----

func TestTorrentsAddRejectsFileScheme(t *testing.T) {
	h := makeQbitHandler("/tmp")

	form := url.Values{"urls": {"file:///etc/passwd?t=get"}}
	req := httptest.NewRequest("POST", "/api/v2/torrents/add",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code == http.StatusOK && strings.Contains(w.Body.String(), "Ok.") {
		t.Error("file:// URL should have been rejected by SSRF protection")
	}
}

func TestTorrentsAddRejectsNonGetDescriptor(t *testing.T) {
	h := makeQbitHandler("/tmp")

	form := url.Values{"urls": {"http://localhost/api?t=search&q=test"}}
	req := httptest.NewRequest("POST", "/api/v2/torrents/add",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// The URL does not have t=get — must be rejected
	if w.Code == http.StatusOK && strings.Contains(w.Body.String(), "Ok.") {
		t.Error("non t=get URL should have been rejected")
	}
}

// ---- Full grab flow: torrents/add → STRM created → torrent completed ----

// makeGrabHandler wires a real strm.Writer and a minimal xtream client
// (using a test server that returns a fixed stream URL) for integration testing.
func makeGrabHandler(t *testing.T, outputDir string) (*Handler, *httptest.Server) {
	t.Helper()

	// Serve a fixed Newznab descriptor
	descriptor := `{
		"xtream_id":42,
		"type":"movie",
		"name":"Inception",
		"year":"2010",
		"imdb_id":"tt1375666",
		"tvdb_id":"",
		"tmdb_id":"27205",
		"container_ext":"mkv",
		"episodes":[]
	}`
	descriptorSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") != "get" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, descriptor)
	}))

	writer := strm.NewWriter(outputDir, "movies", "tv")
	xc := xtream.NewClient(descriptorSrv.URL, "user", "pass")

	store := NewStore()
	h := NewHandler(store, writer, xc, outputDir, "", "")
	return h, descriptorSrv
}

func TestTorrentsAddMovieCreatesSTRM(t *testing.T) {
	outputDir := t.TempDir()
	h, descriptorSrv := makeGrabHandler(t, outputDir)
	defer descriptorSrv.Close()

	// Build the descriptor URL pointing at our test server
	descriptorURL := fmt.Sprintf("%s/api?t=get&id=42&type=movie", descriptorSrv.URL)

	form := url.Values{"urls": {descriptorURL}}
	req := httptest.NewRequest("POST", "/api/v2/torrents/add",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("torrents/add: status = %d, want 200; body=%q", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Ok.") {
		t.Fatalf("expected 'Ok.', got %q", w.Body.String())
	}

	// Poll until the goroutine completes the STRM (allow up to 3 seconds)
	hash := ""
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		torrents := h.store.All()
		if len(torrents) > 0 {
			hash = torrents[0].Hash
			if torrents[0].State == StatePausedUP {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}

	if hash == "" {
		t.Fatal("no torrent added to store")
	}

	torrent := h.store.Get(hash)
	if torrent == nil {
		t.Fatal("torrent disappeared from store")
	}
	if torrent.State != StatePausedUP {
		t.Errorf("torrent state = %q, want pausedUP (STRM should have been written)", torrent.State)
	}
	if len(torrent.StrmPaths) == 0 {
		t.Fatal("no STRM paths recorded on torrent")
	}

	// Verify the file actually exists on disk
	strmPath := torrent.StrmPaths[0]
	if _, err := os.Stat(strmPath); os.IsNotExist(err) {
		t.Errorf("STRM file not found on disk: %s", strmPath)
	}

	// Verify the file is inside the output directory
	if !strings.HasPrefix(strmPath, outputDir) {
		t.Errorf("STRM file %q is outside output dir %q", strmPath, outputDir)
	}

	// Verify the STRM file contains a URL
	content, err := os.ReadFile(strmPath)
	if err != nil {
		t.Fatalf("reading STRM file: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(content)), "http") {
		t.Errorf("STRM content should be a URL, got: %q", string(content))
	}
}

func TestTorrentsInfoShowsCompletedTorrent(t *testing.T) {
	outputDir := t.TempDir()
	h, descriptorSrv := makeGrabHandler(t, outputDir)
	defer descriptorSrv.Close()

	descriptorURL := fmt.Sprintf("%s/api?t=get&id=42&type=movie", descriptorSrv.URL)
	form := url.Values{"urls": {descriptorURL}}
	req := httptest.NewRequest("POST", "/api/v2/torrents/add",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(httptest.NewRecorder(), req)

	// Wait for completion
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		torrents := h.store.All()
		if len(torrents) > 0 && torrents[0].State == StatePausedUP {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Query torrents/info
	req2 := httptest.NewRequest("GET", "/api/v2/torrents/info", nil)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)

	var entries []struct {
		State      string  `json:"state"`
		Progress   float64 `json:"progress"`
		AmountLeft int64   `json:"amount_left"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &entries); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 torrent in info, got %d", len(entries))
	}
	if entries[0].State != string(StatePausedUP) {
		t.Errorf("state = %q, want pausedUP", entries[0].State)
	}
	if entries[0].Progress != 1.0 {
		t.Errorf("progress = %f, want 1.0", entries[0].Progress)
	}
	if entries[0].AmountLeft != 0 {
		t.Errorf("amount_left = %d, want 0", entries[0].AmountLeft)
	}
}

// ---- contentPathForTorrent ----

func TestContentPathSingleFile(t *testing.T) {
	tor := &Torrent{
		SavePath:  "/data/strm",
		StrmPaths: []string{"/data/strm/movies/Inception (2010)/Inception.2010.WEB-DL.mkv.strm"},
	}
	got := contentPathForTorrent(tor)
	want := "/data/strm/movies/Inception (2010)/Inception.2010.WEB-DL.mkv.strm"
	if got != want {
		t.Errorf("single file: contentPath = %q, want %q", got, want)
	}
}

func TestContentPathMultipleFiles(t *testing.T) {
	tor := &Torrent{
		SavePath: "/data/strm",
		StrmPaths: []string{
			"/data/strm/tv/Breaking Bad/Season 01/Breaking.Bad.S01E01.strm",
			"/data/strm/tv/Breaking Bad/Season 01/Breaking.Bad.S01E02.strm",
		},
	}
	got := contentPathForTorrent(tor)
	// Common ancestor of both files is the Season 01 directory
	want := filepath.Dir("/data/strm/tv/Breaking Bad/Season 01/Breaking.Bad.S01E01.strm")
	if got != want {
		t.Errorf("multiple files: contentPath = %q, want %q", got, want)
	}
}

func TestContentPathNoFiles(t *testing.T) {
	tor := &Torrent{SavePath: "/data/strm"}
	got := contentPathForTorrent(tor)
	if got != "/data/strm" {
		t.Errorf("no files: contentPath = %q, want /data/strm", got)
	}
}

// ---- torrents/properties ----

func TestTorrentsProperties(t *testing.T) {
	h := makeQbitHandler("/data/strm")
	h.store.Add(&Torrent{
		Hash:     "props123",
		Name:     "Test",
		SavePath: "/data/strm",
		State:    StatePausedUP,
		Size:     1024 * 1024 * 1024,
	})

	req := httptest.NewRequest("GET", "/api/v2/torrents/properties?hash=props123", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var props map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &props); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := props["save_path"]; !ok {
		t.Error("properties missing save_path field")
	}
}

func TestTorrentsPropertiesUnknownHash(t *testing.T) {
	h := makeQbitHandler("/data/strm")
	req := httptest.NewRequest("GET", "/api/v2/torrents/properties?hash=doesnotexist", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("unknown hash: status = %d, want 404", w.Code)
	}
}

// ---- torrents/info hash filter ----

func TestTorrentsInfoHashFilter(t *testing.T) {
	h := makeQbitHandler("/data/strm")
	h.store.Add(&Torrent{Hash: "aaa", Name: "A", SavePath: "/data/strm", State: StatePausedUP})
	h.store.Add(&Torrent{Hash: "bbb", Name: "B", SavePath: "/data/strm", State: StatePausedUP})

	req := httptest.NewRequest("GET", "/api/v2/torrents/info?hashes=aaa", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var entries []struct{ Hash string `json:"hash"` }
	json.Unmarshal(w.Body.Bytes(), &entries)
	if len(entries) != 1 {
		t.Fatalf("hash filter: expected 1 torrent, got %d", len(entries))
	}
	if entries[0].Hash != "aaa" {
		t.Errorf("hash = %q, want aaa", entries[0].Hash)
	}
}
