package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPIResponsesDisableCaching(t *testing.T) {
	response := httptest.NewRecorder()
	NewRouter(Dependencies{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/missing", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d", response.Code)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control=%q", got)
	}
}

func TestNonAPIResponseDoesNotReceiveStudentCachePolicy(t *testing.T) {
	response := httptest.NewRecorder()
	NewRouter(Dependencies{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d", response.Code)
	}
	if got := response.Header().Get("Cache-Control"); got != "" {
		t.Fatalf("non-API Cache-Control=%q", got)
	}
}
