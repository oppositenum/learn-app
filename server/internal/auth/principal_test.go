package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireRole(t *testing.T) {
	target := RequireRole(RoleStudent, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))

	tests := []struct {
		name      string
		principal *Principal
		want      int
	}{
		{name: "missing", want: http.StatusUnauthorized},
		{name: "wrong role", principal: &Principal{UserID: "parent", Role: RoleParent}, want: http.StatusForbidden},
		{name: "student", principal: &Principal{UserID: "student", Role: RoleStudent}, want: http.StatusNoContent},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			if test.principal != nil {
				request = request.WithContext(WithPrincipal(request.Context(), *test.principal))
			}
			response := httptest.NewRecorder()
			target.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("expected status %d, got %d", test.want, response.Code)
			}
		})
	}
}
