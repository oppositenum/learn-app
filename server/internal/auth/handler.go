package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const sessionLifetime = 7 * 24 * time.Hour

type Handler struct {
	pool          *pgxpool.Pool
	now           func() time.Time
	secureCookies bool
}

func NewHandler(pool *pgxpool.Pool) *Handler { return &Handler{pool: pool, now: time.Now} }

func NewHandlerWithSecureCookies(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool, now: time.Now, secureCookies: true}
}

func (handler *Handler) Login(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		http.Error(writer, "invalid login", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		http.Error(writer, "invalid login", http.StatusBadRequest)
		return
	}
	email := strings.ToLower(strings.TrimSpace(body.Email))
	var userID uuid.UUID
	var role Role
	var displayName, passwordHash string
	var lockedUntil *time.Time
	err := handler.pool.QueryRow(request.Context(), `
SELECT u.id,u.role_code,u.display_name,c.password_hash,c.locked_until
FROM users u JOIN user_credentials c ON c.user_id=u.id WHERE u.email=$1`, email).Scan(&userID, &role, &displayName, &passwordHash, &lockedUntil)
	now := handler.now()
	if err != nil || lockedUntil != nil && lockedUntil.After(now) || !VerifyPassword(passwordHash, body.Password) {
		if err == nil {
			_, _ = handler.pool.Exec(request.Context(), `UPDATE user_credentials SET failed_attempts=failed_attempts+1,locked_until=CASE WHEN failed_attempts+1>=8 THEN now()+interval '15 minutes' ELSE locked_until END WHERE user_id=$1`, userID)
		}
		http.Error(writer, "email or password is invalid", http.StatusUnauthorized)
		return
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		http.Error(writer, "login unavailable", http.StatusInternalServerError)
		return
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	tokenHash := sha256.Sum256([]byte(token))
	expiresAt := now.Add(sessionLifetime)
	if err := pgx.BeginFunc(request.Context(), handler.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(request.Context(), `INSERT INTO sessions(id,user_id,token_hash,expires_at) VALUES($1,$2,$3,$4)`, uuid.New(), userID, tokenHash[:], expiresAt); err != nil {
			return err
		}
		_, err := tx.Exec(request.Context(), `UPDATE user_credentials SET failed_attempts=0,locked_until=NULL WHERE user_id=$1`, userID)
		return err
	}); err != nil {
		http.Error(writer, "login unavailable", http.StatusInternalServerError)
		return
	}
	setSessionCookie(writer, request, token, expiresAt, handler.secureCookies)
	writeAuthJSON(writer, http.StatusOK, map[string]any{"user_id": userID, "role": role, "display_name": displayName})
}

func (handler *Handler) Me(writer http.ResponseWriter, request *http.Request) {
	principal, ok := PrincipalFromContext(request.Context())
	if !ok {
		http.Error(writer, "authentication required", http.StatusUnauthorized)
		return
	}
	var displayName string
	var studentID *uuid.UUID
	err := handler.pool.QueryRow(request.Context(), `SELECT u.display_name,st.id FROM users u LEFT JOIN students st ON st.user_id=u.id WHERE u.id=$1`, principal.UserID).Scan(&displayName, &studentID)
	if err != nil {
		http.Error(writer, "account unavailable", http.StatusUnauthorized)
		return
	}
	writeAuthJSON(writer, http.StatusOK, map[string]any{"user_id": principal.UserID, "role": principal.Role, "display_name": displayName, "student_id": studentID})
}

func (handler *Handler) Logout(writer http.ResponseWriter, request *http.Request) {
	if token, ok := requestToken(request); ok {
		tokenHash := sha256.Sum256([]byte(token))
		_, _ = handler.pool.Exec(request.Context(), `UPDATE sessions SET revoked_at=now() WHERE token_hash=$1 AND revoked_at IS NULL`, tokenHash[:])
	}
	http.SetCookie(writer, &http.Cookie{Name: "session_token", Value: "", Path: "/", HttpOnly: true, Secure: handler.secureCookies || request.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(1, 0)})
	writer.WriteHeader(http.StatusNoContent)
}

func setSessionCookie(writer http.ResponseWriter, request *http.Request, token string, expiresAt time.Time, forceSecure bool) {
	http.SetCookie(writer, &http.Cookie{Name: "session_token", Value: token, Path: "/", HttpOnly: true, Secure: forceSecure || request.TLS != nil, SameSite: http.SameSiteStrictMode, Expires: expiresAt, MaxAge: int(time.Until(expiresAt).Seconds())})
}

func writeAuthJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
