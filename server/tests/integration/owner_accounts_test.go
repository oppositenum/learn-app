package integration

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/oppositenum/ai-learning-tutor/server/internal/api"
	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

func TestOwnerCreatesStudentParentAndSupervisionLink(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	ownerToken := seedAccountTestSession(t, ctx, pool, auth.RoleOwner)
	studentToken := seedAccountTestSession(t, ctx, pool, auth.RoleStudent)
	identity := auth.NewHandler(pool)
	router := api.NewRouter(api.Dependencies{Authenticate: auth.NewSessionAuthenticator(pool).Middleware, Identity: identity})

	studentPassword := "Student account 2026!"
	studentResponse := performJSON(router, http.MethodPost, "/api/v1/owner/accounts/students", ownerToken, map[string]any{
		"email": " NEW.STUDENT@example.test ", "display_name": " 新学生 ", "password": studentPassword, "grade_level": 6,
	})
	if studentResponse.Code != http.StatusCreated {
		t.Fatalf("create student=%d %s", studentResponse.Code, studentResponse.Body.String())
	}
	if strings.Contains(studentResponse.Body.String(), studentPassword) || strings.Contains(studentResponse.Body.String(), "password_hash") {
		t.Fatalf("student creation leaked credentials: %s", studentResponse.Body.String())
	}
	var student struct {
		UserID    uuid.UUID `json:"user_id"`
		StudentID uuid.UUID `json:"student_id"`
	}
	if err := json.Unmarshal(studentResponse.Body.Bytes(), &student); err != nil || student.UserID == uuid.Nil || student.StudentID == uuid.Nil {
		t.Fatalf("student response invalid: %v %s", err, studentResponse.Body.String())
	}
	assertPasswordLoginRole(t, router, "new.student@example.test", studentPassword, auth.RoleStudent)

	parentPassword := "Parent account 2026!"
	parentResponse := performJSON(router, http.MethodPost, "/api/v1/owner/accounts/parents", ownerToken, map[string]any{
		"email": "parent@example.test", "display_name": "测试家长", "password": parentPassword, "student_ids": []string{student.StudentID.String()},
	})
	if parentResponse.Code != http.StatusCreated {
		t.Fatalf("create parent=%d %s", parentResponse.Code, parentResponse.Body.String())
	}
	if strings.Contains(parentResponse.Body.String(), parentPassword) || strings.Contains(parentResponse.Body.String(), "password_hash") {
		t.Fatalf("parent creation leaked credentials: %s", parentResponse.Body.String())
	}
	var parent struct {
		UserID uuid.UUID `json:"user_id"`
	}
	if err := json.Unmarshal(parentResponse.Body.Bytes(), &parent); err != nil || parent.UserID == uuid.Nil {
		t.Fatalf("parent response invalid: %v %s", err, parentResponse.Body.String())
	}
	assertPasswordLoginRole(t, router, "parent@example.test", parentPassword, auth.RoleParent)

	accounts := performJSON(router, http.MethodGet, "/api/v1/owner/accounts", ownerToken, nil)
	if accounts.Code != http.StatusOK || !strings.Contains(accounts.Body.String(), "new.student@example.test") || !strings.Contains(accounts.Body.String(), "parent@example.test") || !strings.Contains(accounts.Body.String(), student.StudentID.String()) {
		t.Fatalf("accounts=%d %s", accounts.Code, accounts.Body.String())
	}
	if strings.Contains(accounts.Body.String(), "password") || strings.Contains(accounts.Body.String(), studentPassword) || strings.Contains(accounts.Body.String(), parentPassword) {
		t.Fatalf("account list leaked credentials: %s", accounts.Body.String())
	}

	forbidden := performJSON(router, http.MethodGet, "/api/v1/owner/accounts", studentToken, nil)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("student read owner accounts=%d %s", forbidden.Code, forbidden.Body.String())
	}

	duplicate := performJSON(router, http.MethodPost, "/api/v1/owner/accounts/students", ownerToken, map[string]any{
		"email": "new.student@example.test", "display_name": "重复", "password": "Duplicate account 2026!", "grade_level": 7,
	})
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate email=%d %s", duplicate.Code, duplicate.Body.String())
	}

	missingStudent := uuid.New()
	missingParent := performJSON(router, http.MethodPost, "/api/v1/owner/accounts/parents", ownerToken, map[string]any{
		"email": "orphan@example.test", "display_name": "未绑定家长", "password": "Orphan account 2026!", "student_ids": []string{missingStudent.String()},
	})
	if missingParent.Code != http.StatusNotFound {
		t.Fatalf("missing student parent=%d %s", missingParent.Code, missingParent.Body.String())
	}
	var orphanCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE email='orphan@example.test'`).Scan(&orphanCount); err != nil || orphanCount != 0 {
		t.Fatalf("parent transaction did not roll back: count=%d err=%v", orphanCount, err)
	}

	secondStudent := performJSON(router, http.MethodPost, "/api/v1/owner/accounts/students", ownerToken, map[string]any{
		"email": "second.student@example.test", "display_name": "第二位学生", "password": "Second student 2026!", "grade_level": 8,
	})
	var second struct {
		StudentID uuid.UUID `json:"student_id"`
	}
	if secondStudent.Code != http.StatusCreated || json.Unmarshal(secondStudent.Body.Bytes(), &second) != nil {
		t.Fatalf("second student=%d %s", secondStudent.Code, secondStudent.Body.String())
	}
	link := performJSON(router, http.MethodPost, "/api/v1/owner/accounts/links", ownerToken, map[string]any{"parent_user_id": parent.UserID, "student_id": second.StudentID})
	if link.Code != http.StatusCreated {
		t.Fatalf("create link=%d %s", link.Code, link.Body.String())
	}
	var activeLinks int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM parent_student_links WHERE parent_user_id=$1 AND status='ACTIVE'`, parent.UserID).Scan(&activeLinks); err != nil || activeLinks != 2 {
		t.Fatalf("active links=%d err=%v", activeLinks, err)
	}
}

func TestOwnerAccountPayloadValidation(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	ownerToken := seedAccountTestSession(t, ctx, pool, auth.RoleOwner)
	router := api.NewRouter(api.Dependencies{Authenticate: auth.NewSessionAuthenticator(pool).Middleware, Identity: auth.NewHandler(pool)})
	tests := []map[string]any{
		{"email": "not-an-email", "display_name": "学生", "password": "Valid password 2026!", "grade_level": 7},
		{"email": "student@example.test", "display_name": "学生", "password": "ab", "grade_level": 7},
		{"email": "student@example.test", "display_name": "学生", "password": "Valid password 2026!", "grade_level": 10},
		{"email": "student@example.test", "display_name": "学生", "password": "Valid password 2026!", "grade_level": 7, "role": "OWNER"},
	}
	for _, body := range tests {
		response := performJSON(router, http.MethodPost, "/api/v1/owner/accounts/students", ownerToken, body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid payload accepted=%d %s", response.Code, response.Body.String())
		}
	}
}

func seedAccountTestSession(t *testing.T, ctx context.Context, pool interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, role auth.Role) string {
	t.Helper()
	userID, token := uuid.New(), strings.ToLower(string(role))+"-"+uuid.NewString()
	hash := sha256.Sum256([]byte(token))
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,role_code,display_name)VALUES($1,$2,$3)`, userID, role, string(role)+" test"); err != nil {
		t.Fatal(err)
	}
	if role == auth.RoleStudent {
		if _, err := pool.Exec(ctx, `INSERT INTO students(id,user_id,grade_level)VALUES($1,$2,7)`, uuid.New(), userID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO sessions(id,user_id,token_hash,expires_at)VALUES($1,$2,$3,$4)`, uuid.New(), userID, hash[:], time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return token
}

func assertPasswordLoginRole(t *testing.T, router http.Handler, email, password string, role auth.Role) {
	t.Helper()
	response := performJSON(router, http.MethodPost, "/api/v1/auth/login", "", map[string]any{"email": email, "password": password})
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"role":"`+string(role)+`"`) {
		t.Fatalf("login %s=%d %s", email, response.Code, response.Body.String())
	}
}
