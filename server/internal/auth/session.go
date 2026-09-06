package auth

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrSessionRevoked = errors.New("authentication session is no longer active")

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
	SELECT u.id::text, s.id::text, u.role_code
	FROM sessions s
	JOIN users u ON u.id = s.user_id
	WHERE s.token_hash = $1 AND s.revoked_at IS NULL AND s.expires_at > now()`, tokenHash[:]).Scan(
			&principal.UserID,
			&principal.AuthSessionID,
			&principal.Role,
		)
		if err != nil || !principal.Role.Valid() {
			http.Error(writer, "authentication required", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(writer, request.WithContext(WithPrincipal(request.Context(), principal)))
	})
}

// LockPrincipalSession serializes request writes with logout. Trusted internal
// callers without an HTTP principal continue to use the service APIs directly.
func LockPrincipalSession(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	principal, ok := PrincipalFromContext(ctx)
	if !ok {
		return nil
	}
	if principal.UserID != userID.String() {
		return ErrSessionRevoked
	}
	authSessionID, err := uuid.Parse(principal.AuthSessionID)
	if err != nil {
		return ErrSessionRevoked
	}
	var locked uuid.UUID
	err = tx.QueryRow(ctx, `
SELECT id
FROM sessions
WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL AND expires_at>now()
FOR UPDATE`, authSessionID, userID).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrSessionRevoked
	}
	return err
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
