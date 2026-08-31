package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBearerToken(t *testing.T) {
	tests := []struct {
		header string
		want   string
		ok     bool
	}{
		{header: "Bearer secret", want: "secret", ok: true},
		{header: "bearer secret", want: "secret", ok: true},
		{header: "Basic secret", ok: false},
		{header: "Bearer", ok: false},
		{header: "Bearer   ", ok: false},
	}
	for _, test := range tests {
		got, ok := bearerToken(test.header)
		if ok != test.ok || got != test.want {
			t.Fatalf("bearerToken(%q) = (%q, %v), want (%q, %v)", test.header, got, ok, test.want, test.ok)
		}
	}
}

func TestRequestTokenSupportsHttpOnlyCookieTransport(t *testing.T) {
	request := httptest.NewRequest("GET", "/ws/parent/student", nil)
	request.AddCookie(&http.Cookie{Name: "session_token", Value: "cookie-secret", HttpOnly: true})
	token, ok := requestToken(request)
	if !ok || token != "cookie-secret" {
		t.Fatalf("requestToken = (%q, %v)", token, ok)
	}
}
