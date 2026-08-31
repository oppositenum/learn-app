package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/oppositenum/ai-learning-tutor/server/internal/api"
	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

func TestPasswordLoginUsesSecureHttpOnlyCookieAndServerSideRevocation(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	password := "student test password 123"
	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	userID, studentID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,role_code,email,display_name)VALUES($1,'STUDENT','student@example.test','小航')`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO students(id,user_id,grade_level)VALUES($1,$2,7)`, studentID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_credentials(user_id,password_hash)VALUES($1,$2)`, userID, passwordHash); err != nil {
		t.Fatal(err)
	}
	authenticator := auth.NewSessionAuthenticator(pool)
	router := api.NewRouter(api.Dependencies{Authenticate: authenticator.Middleware, Identity: auth.NewHandlerWithSecureCookies(pool)})

	wrong := performJSON(router, http.MethodPost, "/api/v1/auth/login", "", map[string]any{"email": "student@example.test", "password": "wrong password value"})
	if wrong.Code != http.StatusUnauthorized || strings.Contains(wrong.Body.String(), "student@example.test") {
		t.Fatalf("wrong login=%d %s", wrong.Code, wrong.Body.String())
	}
	login := performJSON(router, http.MethodPost, "/api/v1/auth/login", "", map[string]any{"email": "STUDENT@example.test ", "password": password})
	if login.Code != http.StatusOK || strings.Contains(login.Body.String(), "session_token") || !strings.Contains(login.Body.String(), `"role":"STUDENT"`) {
		t.Fatalf("login=%d %s", login.Code, login.Body.String())
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "session_token" || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != "/" {
		t.Fatalf("session cookie=%+v", cookies)
	}
	meRequest := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	meRequest.AddCookie(cookies[0])
	me := httptest.NewRecorder()
	router.ServeHTTP(me, meRequest)
	if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), studentID.String()) || strings.Contains(me.Body.String(), password) {
		t.Fatalf("me=%d %s", me.Code, me.Body.String())
	}

	logoutRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logoutRequest.AddCookie(cookies[0])
	logout := httptest.NewRecorder()
	router.ServeHTTP(logout, logoutRequest)
	if logout.Code != http.StatusNoContent || len(logout.Result().Cookies()) != 1 || logout.Result().Cookies()[0].MaxAge >= 0 || !logout.Result().Cookies()[0].Secure {
		t.Fatalf("logout=%d cookie=%+v", logout.Code, logout.Result().Cookies())
	}
	me = httptest.NewRecorder()
	router.ServeHTTP(me, meRequest)
	if me.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session still authenticated: %d %s", me.Code, me.Body.String())
	}
}
