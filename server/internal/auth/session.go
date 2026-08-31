package auth

import (
	"crypto/sha256"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

type SessionAuthenticator struct {
	pool *pgxpool.Pool
}

func NewSessionAuthenticator(pool *pgxpool.Pool) *SessionAuthenticator {
	return &SessionAuthenticator{pool: pool}
}

func (authenticator *SessionAuthenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		token, ok := requestToken(request)
		if !ok {
			http.Error(writer, "authentication required", http.StatusUnauthorized)
			return
		}

		tokenHash := sha256.Sum256([]byte(token))
		var principal Principal
		err := authenticator.pool.QueryRow(request.Context(), `
SELECT u.id::text, u.role_code
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token_hash = $1 AND s.revoked_at IS NULL AND s.expires_at > now()`, tokenHash[:]).Scan(
			&principal.UserID,
			&principal.Role,
		)
		if err != nil || !principal.Role.Valid() {
			http.Error(writer, "authentication required", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(writer, request.WithContext(WithPrincipal(request.Context(), principal)))
	})
}

func requestToken(request *http.Request) (string, bool) {
	if token, ok := bearerToken(request.Header.Get("Authorization")); ok {
		return token, true
	}
	cookie, err := request.Cookie("session_token")
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return "", false
	}
	return strings.TrimSpace(cookie.Value), true
}

func bearerToken(header string) (string, bool) {
	prefix, token, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(prefix, "Bearer") || strings.TrimSpace(token) == "" {
		return "", false
	}
	return strings.TrimSpace(token), true
}
