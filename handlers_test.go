package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func setupTestServer(t *testing.T) *http.ServeMux {
	initPaths()
	if err := initTemplates(); err != nil {
		t.Fatalf("initTemplates failed: %v", err)
	}
	mux := http.NewServeMux()
	registerRoutes(mux)
	return mux
}

func TestIndexPage(t *testing.T) {
	mux := setupTestServer(t)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET / returned status %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Lifel") {
		t.Errorf("GET / response does not contain 'Lifel'")
	}
}

func TestAPIState(t *testing.T) {
	mux := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/state", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /api/state returned %d", w.Code)
	}
	var data map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatalf("GET /api/state returned invalid JSON: %v", err)
	}
	if data["config"] == nil || data["voices"] == nil || data["tools"] == nil {
		t.Errorf("GET /api/state missing expected top-level keys: %v", data)
	}
}

func TestAPIMemoryCRUD(t *testing.T) {
	mux := setupTestServer(t)

	// 1. Add memory via POST
	addPayload := `{"content": "Automated test memory item", "category": "task", "pinned": false}`
	req := httptest.NewRequest("POST", "/api/memory", strings.NewReader(addPayload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/memory failed: %d, body: %s", w.Code, w.Body.String())
	}
	var addResp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &addResp)
	item, ok := addResp["item"].(map[string]any)
	if !ok || item["id"] == nil {
		t.Fatalf("POST /api/memory did not return item: %v", addResp)
	}
	itemID := item["id"].(string)

	// 2. Update memory via PUT
	updatePayload := `{"content": "Updated test memory item", "category": "task"}`
	req = httptest.NewRequest("PUT", "/api/memory/"+itemID, strings.NewReader(updatePayload))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("PUT /api/memory/%s failed: %d", itemID, w.Code)
	}

	// 3. Delete memory via DELETE
	req = httptest.NewRequest("DELETE", "/api/memory/"+itemID, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("DELETE /api/memory/%s failed: %d", itemID, w.Code)
	}
}

func TestHTMXEndpoints(t *testing.T) {
	mux := setupTestServer(t)

	// 1. GET /htmx/memory
	req := httptest.NewRequest("GET", "/htmx/memory", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("GET /htmx/memory returned %d", w.Code)
	}

	// 2. GET /htmx/notes
	req = httptest.NewRequest("GET", "/htmx/notes", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("GET /htmx/notes returned %d", w.Code)
	}

	// 3. GET /htmx/sessions
	req = httptest.NewRequest("GET", "/htmx/sessions", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("GET /htmx/sessions returned %d", w.Code)
	}

	// 4. GET /htmx/keys
	req = httptest.NewRequest("GET", "/htmx/keys", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("GET /htmx/keys returned %d", w.Code)
	}

	// 5. POST /htmx/voice/select
	form := url.Values{"voice_name": {"Charon"}}
	req = httptest.NewRequest("POST", "/htmx/voice/select", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("POST /htmx/voice/select returned %d", w.Code)
	}
}
