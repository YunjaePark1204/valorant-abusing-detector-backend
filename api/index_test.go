package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsAllowedOrigin(t *testing.T) {
	allowed := []string{
		"https://valorant-abusing-frontend.vercel.app",
		"https://preview-branch.vercel.app",
		"http://localhost:5173",
		"http://127.0.0.1:3000",
	}
	for _, origin := range allowed {
		if !isAllowedOrigin(origin) {
			t.Errorf("expected origin %s to be allowed", origin)
		}
	}

	if isAllowedOrigin("https://evil.example.com") {
		t.Errorf("expected unknown origin to be rejected")
	}
}

func TestHandler_PingAndOptionsCORS(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/api/ping", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	w := httptest.NewRecorder()

	Handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on OPTIONS, got %d", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("expected localhost CORS header, got %q", got)
	}

	pingReq := httptest.NewRequest(http.MethodGet, "/api/ping", nil)
	pingW := httptest.NewRecorder()
	Handler(pingW, pingReq)
	if pingW.Code != http.StatusOK {
		t.Fatalf("expected 200 on GET /api/ping, got %d", pingW.Code)
	}
}

func TestGetAccount_RequiresParams(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/account/riotid?gameName=onlyname", nil)
	w := httptest.NewRecorder()
	Handler(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when tagLine missing, got %d", w.Code)
	}
}

func TestMergeUniqueMatches(t *testing.T) {
	m1 := map[string]interface{}{
		"metadata": map[string]interface{}{"matchid": "match-1", "mode": "Competitive"},
		"players":  map[string]interface{}{"all_players": []interface{}{map[string]interface{}{"puuid": "p1"}}},
	}
	m1Dup := map[string]interface{}{
		"metadata": map[string]interface{}{"match_id": "match-1", "mode": "Competitive"},
		"players":  []interface{}{map[string]interface{}{"puuid": "p1"}},
	}
	m2 := map[string]interface{}{
		"metadata": map[string]interface{}{"match_id": "match-2", "mode": "Competitive"},
		"players":  []interface{}{map[string]interface{}{"puuid": "p1"}},
	}

	merged := mergeUniqueMatches([]map[string]interface{}{m1}, []map[string]interface{}{m1Dup, m2})
	if len(merged) != 2 {
		t.Fatalf("expected 2 unique matches, got %d", len(merged))
	}

	upsertCachedMatches("Test-PUUID", []map[string]interface{}{m1})
	upsertCachedMatches("test-puuid", []map[string]interface{}{m1Dup, m2})
	cached := getCachedMatches("TEST-PUUID")
	if len(cached) != 2 {
		t.Fatalf("expected 2 accumulated cached matches, got %d", len(cached))
	}
}
