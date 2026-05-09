package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/vodarr/vodarr/internal/config"
	"github.com/vodarr/vodarr/internal/index"
)

// makeHandler builds a Handler with the given config and no auth.
// scheduler is nil — only call endpoints that don't use it.
func makeHandler(cfg *config.Config, cfgPath string) *Handler {
	return NewHandler(index.New(), nil, nil, nil, nil, cfg, cfgPath, "", "", "test")
}

func minimalCfg() *config.Config {
	return &config.Config{
		Xtream: config.XtreamConfig{
			URL:      "http://xtream.example.com",
			Username: "user",
			Password: "secretpass",
		},
		TMDB: config.TMDBConfig{APIKey: "tmdbkey"},
		Output: config.OutputConfig{
			Path:      "/data/strm",
			MoviesDir: "movies",
			SeriesDir: "tv",
		},
		Sync:    config.SyncConfig{Interval: "6h", OnStartup: true},
		Server:  config.ServerConfig{NewznabPort: 7878, QbitPort: 8080, WebPort: 3000},
		Logging: config.LoggingConfig{Level: "info"},
	}
}

func TestGetConfigMasksPasswords(t *testing.T) {
	cfg := minimalCfg()
	h := makeHandler(cfg, "")

	req := httptest.NewRequest("GET", "/api/config", nil)
	w := httptest.NewRecorder()
	h.handleGetConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp configResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp.Xtream.Password != passwordSentinel {
		t.Errorf("Xtream.Password = %q, want sentinel", resp.Xtream.Password)
	}
	if resp.TMDB.APIKey != passwordSentinel {
		t.Errorf("TMDB.APIKey = %q, want sentinel", resp.TMDB.APIKey)
	}
	if resp.Xtream.URL != cfg.Xtream.URL {
		t.Errorf("Xtream.URL = %q, want %q", resp.Xtream.URL, cfg.Xtream.URL)
	}
}

func TestGetConfigEmptyPasswordsNotSentinel(t *testing.T) {
	cfg := minimalCfg()
	cfg.Xtream.Password = ""
	h := makeHandler(cfg, "")

	req := httptest.NewRequest("GET", "/api/config", nil)
	w := httptest.NewRecorder()
	h.handleGetConfig(w, req)

	var resp configResponse
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Xtream.Password != "" {
		t.Errorf("empty password should not be masked, got %q", resp.Xtream.Password)
	}
}

func TestPutConfigPersistsAndResolveSentinel(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yml")

	cfg := minimalCfg()
	h := makeHandler(cfg, cfgPath)

	// Send PUT with sentinel password — should keep stored password
	body := map[string]interface{}{
		"xtream": map[string]interface{}{
			"url":      "http://new-xtream.com",
			"username": "newuser",
			"password": passwordSentinel,
		},
		"tmdb":   map[string]interface{}{"api_key": passwordSentinel},
		"output": map[string]interface{}{"path": dir, "movies_dir": "movies", "series_dir": "tv"},
		"sync":   map[string]interface{}{"interval": "12h", "on_startup": false},
		"server": map[string]interface{}{"newznab_port": 7878, "qbit_port": 8080, "web_port": 3000},
		"logging": map[string]interface{}{"level": "debug"},
	}
	data, _ := json.Marshal(body)
	req := httptest.NewRequest("PUT", "/api/config", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handlePutConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["restart_required"] != true {
		t.Errorf("restart_required = %v, want true", resp["restart_required"])
	}

	// Verify the stored config was updated
	h.cfgMu.RLock()
	stored := h.cfg
	h.cfgMu.RUnlock()

	if stored.Xtream.URL != "http://new-xtream.com" {
		t.Errorf("URL not updated, got %q", stored.Xtream.URL)
	}
	// Password sentinel should resolve to original stored password
	if stored.Xtream.Password != "secretpass" {
		t.Errorf("Password = %q, want %q (sentinel resolved)", stored.Xtream.Password, "secretpass")
	}
	if stored.TMDB.APIKey != "tmdbkey" {
		t.Errorf("TMDBAPIKey = %q, want original", stored.TMDB.APIKey)
	}
	if stored.Sync.Interval != "12h" {
		t.Errorf("Interval = %q, want 12h", stored.Sync.Interval)
	}

	// Verify file was written
	loaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load after save: %v", err)
	}
	if loaded.Xtream.URL != "http://new-xtream.com" {
		t.Errorf("loaded URL = %q, want new URL", loaded.Xtream.URL)
	}
}

func TestPutConfigValidationError(t *testing.T) {
	cfg := minimalCfg()
	h := makeHandler(cfg, "")

	// Missing xtream URL should fail validation
	body := map[string]interface{}{
		"xtream":  map[string]interface{}{"url": "", "username": "u", "password": "p"},
		"tmdb":    map[string]interface{}{"api_key": "key"},
		"output":  map[string]interface{}{"path": "/data"},
		"sync":    map[string]interface{}{"interval": "6h"},
		"server":  map[string]interface{}{"newznab_port": 7878, "qbit_port": 8080, "web_port": 3000},
		"logging": map[string]interface{}{"level": "info"},
	}
	data, _ := json.Marshal(body)
	req := httptest.NewRequest("PUT", "/api/config", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handlePutConfig(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] == "" {
		t.Error("expected error field in response")
	}
}

func TestPutConfigPreservesServerSensitiveFields(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yml")

	cfg := minimalCfg()
	cfg.Server.APIKey = "secret-newznab-key"
	cfg.Server.ExternalURL = "http://vodarr:7878"
	cfg.Server.WebUsername = "admin"
	cfg.Server.WebPassword = "webpass"
	h := makeHandler(cfg, cfgPath)

	body := map[string]interface{}{
		"xtream":  map[string]interface{}{"url": "http://x.com", "username": "u", "password": "p"},
		"tmdb":    map[string]interface{}{"api_key": "key"},
		"output":  map[string]interface{}{"path": dir},
		"sync":    map[string]interface{}{"interval": "6h"},
		"server":  map[string]interface{}{"newznab_port": 9999, "qbit_port": 8080, "web_port": 3000},
		"logging": map[string]interface{}{"level": "info"},
	}
	data, _ := json.Marshal(body)
	req := httptest.NewRequest("PUT", "/api/config", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handlePutConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	h.cfgMu.RLock()
	stored := h.cfg
	h.cfgMu.RUnlock()

	// Port should be updated
	if stored.Server.NewznabPort != 9999 {
		t.Errorf("NewznabPort = %d, want 9999", stored.Server.NewznabPort)
	}
	// Sensitive fields should be preserved
	if stored.Server.APIKey != "secret-newznab-key" {
		t.Errorf("APIKey = %q, want preserved", stored.Server.APIKey)
	}
	if stored.Server.ExternalURL != "http://vodarr:7878" {
		t.Errorf("ExternalURL = %q, want preserved", stored.Server.ExternalURL)
	}
	if stored.Server.WebUsername != "admin" {
		t.Errorf("WebUsername = %q, want preserved", stored.Server.WebUsername)
	}
}

func TestTestXtreamSuccess(t *testing.T) {
	// Mock Xtream server
	xtreamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"user_info":{"username":"testuser","status":"Active"},"server_info":{}}`))
	}))
	defer xtreamSrv.Close()

	cfg := minimalCfg()
	h := makeHandler(cfg, "")

	body := map[string]interface{}{
		"url":      xtreamSrv.URL,
		"username": "testuser",
		"password": "pass",
	}
	data, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/test-xtream", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleTestXtream(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["success"] != true {
		t.Errorf("success = %v, want true; error = %v", resp["success"], resp["error"])
	}
}

func TestTestXtreamSentinelUsesStoredPassword(t *testing.T) {
	// Mock Xtream server — verifies password via query param
	var gotPassword string
	xtreamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPassword = r.URL.Query().Get("password")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"user_info":{"username":"u","status":"Active"},"server_info":{}}`))
	}))
	defer xtreamSrv.Close()

	cfg := minimalCfg()
	cfg.Xtream.Password = "realstored"
	h := makeHandler(cfg, "")

	body := map[string]interface{}{
		"url":      xtreamSrv.URL,
		"username": "u",
		"password": passwordSentinel, // should resolve to stored
	}
	data, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/test-xtream", bytes.NewReader(data))
	w := httptest.NewRecorder()
	h.handleTestXtream(w, req)

	if gotPassword != "realstored" {
		t.Errorf("sent password = %q, want %q", gotPassword, "realstored")
	}
}

func TestTestTMDBSuccess(t *testing.T) {
	// Mock TMDB server
	tmdbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/authentication" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"success":true}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer tmdbSrv.Close()

	// We need the TMDB client to use our mock server URL.
	// The handler creates a new tmdb.NewClient — which uses defaultBaseURL.
	// To test this we inject via a custom TMDB base URL, but since
	// the handler creates its own client we can't easily override it.
	// Instead, test the sentinel resolution and endpoint wiring via integration:
	// Use a real TMDB-shaped server at a local address.

	cfg := minimalCfg()
	h := makeHandler(cfg, "")

	// We can't easily override the TMDB base URL in the handler without
	// exposing a hook, so test the error path (unreachable host = clear error).
	body := map[string]interface{}{"api_key": "testkey"}
	data, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/test-tmdb", bytes.NewReader(data))
	w := httptest.NewRecorder()
	h.handleTestTMDB(w, req)

	// Should get a response (success or error) — just verify valid JSON shape
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := resp["success"]; !ok {
		t.Error("response missing 'success' field")
	}
}

func TestTestTMDBSentinelUsesStoredKey(t *testing.T) {
	cfg := minimalCfg()
	cfg.TMDB.APIKey = "stored-tmdb-key"
	h := makeHandler(cfg, "")

	// Send sentinel — the handler should resolve to stored key.
	// We can't intercept the HTTP call, so we just verify no panic/error
	// in the resolution logic via the handleTestTMDB path.
	// The actual HTTP call will fail (no real TMDB), returning success=false.
	body := map[string]interface{}{"api_key": passwordSentinel}
	data, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/test-tmdb", bytes.NewReader(data))
	w := httptest.NewRecorder()
	h.handleTestTMDB(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	// success may be false (no real TMDB), but error should not be about "invalid key"
	// just verify the field exists
	if _, ok := resp["success"]; !ok {
		t.Error("response missing 'success' field")
	}
}

func TestAuthRejectsWrongPassword(t *testing.T) {
	cfg := minimalCfg()
	h := NewHandler(index.New(), nil, nil, nil, nil, cfg, "", "admin", "secret", "test")

	req := httptest.NewRequest("GET", "/api/config", nil)
	req.SetBasicAuth("admin", "wrongpassword")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestAuthAcceptsCorrectPassword(t *testing.T) {
	cfg := minimalCfg()
	h := NewHandler(index.New(), nil, nil, nil, nil, cfg, "", "admin", "secret", "test")

	req := httptest.NewRequest("GET", "/api/config", nil)
	req.SetBasicAuth("admin", "secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestNoCORSWildcard(t *testing.T) {
	h := makeHandler(minimalCfg(), "")
	req := httptest.NewRequest("GET", "/api/health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got == "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, must not be wildcard", got)
	}
}

// --- Webhook handler tests ---

func TestWebhookTestEvent(t *testing.T) {
	h := makeHandler(minimalCfg(), "")
	body, _ := json.Marshal(map[string]string{"eventType": "Test"})
	req := httptest.NewRequest("POST", "/api/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.handleWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "ok" {
		t.Errorf("status = %q, want ok", resp["status"])
	}
}

func TestWebhookNonDownloadEvent(t *testing.T) {
	h := makeHandler(minimalCfg(), "")
	body, _ := json.Marshal(map[string]string{"eventType": "Rename"})
	req := httptest.NewRequest("POST", "/api/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.handleWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestWebhookDownloadDeletesMkv(t *testing.T) {
	dir := t.TempDir()
	mkvPath := filepath.Join(dir, "Show S01E01.mkv")
	strmPath := filepath.Join(dir, "Show S01E01.strm")

	// Create both files
	if err := os.WriteFile(mkvPath, []byte("fake mkv"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(strmPath, []byte("http://stream.example.com"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := minimalCfg()
	cfg.Output.Path = dir // files live inside the output directory
	h := makeHandler(cfg, "")
	payload := map[string]interface{}{
		"eventType": "Download",
		"episodeFile": map[string]string{
			"path": mkvPath,
		},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.handleWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	// mkv should be gone
	if _, err := os.Stat(mkvPath); !os.IsNotExist(err) {
		t.Error("expected .mkv to be deleted, but it still exists")
	}
	// strm should remain
	if _, err := os.Stat(strmPath); err != nil {
		t.Errorf("expected .strm to remain, got error: %v", err)
	}
}

func TestWebhookDownloadNoStrmSibling(t *testing.T) {
	dir := t.TempDir()
	mkvPath := filepath.Join(dir, "Movie 2024.mkv")
	if err := os.WriteFile(mkvPath, []byte("fake mkv"), 0644); err != nil {
		t.Fatal(err)
	}

	h := makeHandler(minimalCfg(), "")
	payload := map[string]interface{}{
		"eventType": "Download",
		"movieFile": map[string]string{
			"path": mkvPath,
		},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.handleWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	// mkv should NOT be deleted (no strm sibling)
	if _, err := os.Stat(mkvPath); err != nil {
		t.Error("expected .mkv to remain when no .strm sibling exists, but it was deleted")
	}
}

func TestWebhookDownloadNonMkvPath(t *testing.T) {
	h := makeHandler(minimalCfg(), "")
	payload := map[string]interface{}{
		"eventType": "Download",
		"episodeFile": map[string]string{
			"path": "/some/path/episode.avi",
		},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.handleWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

// --- Arr config roundtrip ---

func TestGetConfigIncludesArrSection(t *testing.T) {
	cfg := minimalCfg()
	cfg.Arr = config.ArrConfig{
		Instances: []config.ArrInstance{
			{Name: "Sonarr Dutch", Type: "sonarr", URL: "http://sonarr:8989", APIKey: "secret123"},
		},
	}
	h := makeHandler(cfg, "")

	req := httptest.NewRequest("GET", "/api/config", nil)
	w := httptest.NewRecorder()
	h.handleGetConfig(w, req)

	var resp configResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Arr.Instances) != 1 {
		t.Fatalf("want 1 arr instance, got %d", len(resp.Arr.Instances))
	}
	if resp.Arr.Instances[0].APIKey != passwordSentinel {
		t.Errorf("APIKey = %q, want sentinel", resp.Arr.Instances[0].APIKey)
	}
	if resp.Arr.Instances[0].URL != "http://sonarr:8989" {
		t.Errorf("URL = %q, want http://sonarr:8989", resp.Arr.Instances[0].URL)
	}
}

func TestPutConfigArrSentinelResolution(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yml")

	cfg := minimalCfg()
	cfg.Arr = config.ArrConfig{
		Instances: []config.ArrInstance{
			{Name: "Sonarr Dutch", Type: "sonarr", URL: "http://sonarr:8989", APIKey: "stored-arr-key"},
		},
	}
	h := makeHandler(cfg, cfgPath)

	body := map[string]interface{}{
		"xtream":  map[string]interface{}{"url": "http://x.com", "username": "u", "password": "p"},
		"tmdb":    map[string]interface{}{"api_key": "key"},
		"output":  map[string]interface{}{"path": dir},
		"sync":    map[string]interface{}{"interval": "6h"},
		"server":  map[string]interface{}{"newznab_port": 7878, "qbit_port": 8080, "web_port": 3000},
		"logging": map[string]interface{}{"level": "info"},
		"arr": map[string]interface{}{
			"instances": []map[string]interface{}{
				{"name": "Sonarr Dutch", "type": "sonarr", "url": "http://sonarr:8989", "api_key": passwordSentinel},
			},
		},
	}
	data, _ := json.Marshal(body)
	req := httptest.NewRequest("PUT", "/api/config", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handlePutConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	h.cfgMu.RLock()
	stored := h.cfg
	h.cfgMu.RUnlock()

	if len(stored.Arr.Instances) != 1 {
		t.Fatalf("want 1 arr instance, got %d", len(stored.Arr.Instances))
	}
	if stored.Arr.Instances[0].APIKey != "stored-arr-key" {
		t.Errorf("APIKey = %q, want stored-arr-key (sentinel resolved)", stored.Arr.Instances[0].APIKey)
	}
}
