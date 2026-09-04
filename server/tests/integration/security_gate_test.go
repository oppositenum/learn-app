package integration

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oppositenum/ai-learning-tutor/server/internal/api"
	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
	"github.com/oppositenum/ai-learning-tutor/server/internal/classroom"
	"github.com/oppositenum/ai-learning-tutor/server/internal/content"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	parentrepo "github.com/oppositenum/ai-learning-tutor/server/internal/parent"
	"github.com/oppositenum/ai-learning-tutor/server/internal/planner"
	"github.com/oppositenum/ai-learning-tutor/server/internal/realtime"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

func TestIntegrationDatabaseConfiguredInCI(t *testing.T) {
	if os.Getenv("CI") == "" {
		t.Skip("CI-only PostgreSQL configuration gate")
	}
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Fatal("TEST_DATABASE_URL must be configured in CI so PostgreSQL safety tests cannot silently skip")
	}
}

func TestPostgresStudentAnswerNonDisclosureGate(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	schema := "test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	adminPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("create admin pool: %v", err)
	}
	defer adminPool.Close()

	if _, err := adminPool.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminPool.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
	})

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse database config: %v", err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("create test pool: %v", err)
	}
	defer pool.Close()

	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatalf("migrations are not idempotent: %v", err)
	}

	assertFiveSubjectsSeeded(t, ctx, pool)
	fixture := seedSecurityFixture(t, ctx, pool)
	assertParentStudentBinding(t, ctx, pool, fixture)
	assertPrivateAnswerExists(t, ctx, pool, fixture.releasedQuestionID)

	authenticator := auth.NewSessionAuthenticator(pool)
	parents := parentrepo.NewRepository(pool)
	hub := realtime.NewHub()
	plannerService := planner.NewService(pool)
	classroomService := classroom.NewService(pool, hub, nil, nil, plannerService)
	router := api.NewRouter(api.Dependencies{
		Authenticate:    authenticator.Middleware,
		PublicQuestions: content.NewRepository(pool),
		Parents:         parents,
		Classroom:       classroom.NewHandler(classroomService, pool, parents, plannerService),
	})

	response := performQuestionRequest(router, fixture.studentToken, fixture.releasedQuestionID)
	if response.Code != http.StatusOK {
		t.Fatalf("student released question status = %d: %s", response.Code, response.Body.String())
	}
	assertStudentPayloadHasNoPrivateFields(t, response.Body.Bytes())
	if strings.Contains(response.Body.String(), fixture.privateCanary) {
		t.Fatalf("student response leaked private answer canary: %s", response.Body.String())
	}

	response = performQuestionRequest(router, fixture.parentToken, fixture.releasedQuestionID)
	if response.Code != http.StatusForbidden {
		t.Fatalf("parent calling student endpoint status = %d, want %d", response.Code, http.StatusForbidden)
	}

	response = performQuestionRequest(router, fixture.studentToken, fixture.draftQuestionID)
	if response.Code != http.StatusNotFound {
		t.Fatalf("draft question status = %d, want %d", response.Code, http.StatusNotFound)
	}

	for _, path := range []string{
		"/api/v1/student/sessions/current",
		"/api/v1/student/sessions/" + fixture.sessionID.String(),
	} {
		response = performJSON(router, http.MethodGet, path, fixture.studentToken, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("student classroom GET %s = %d: %s", path, response.Code, response.Body.String())
		}
		assertStudentPayloadHasNoPrivateFields(t, response.Body.Bytes())
		if strings.Contains(response.Body.String(), fixture.privateCanary) {
			t.Fatalf("student classroom GET leaked private answer canary: %s", response.Body.String())
		}
	}

	studentEvents, unsubscribe := hub.Subscribe(fixture.studentID.String(), auth.RoleStudent)
	defer unsubscribe()
	for _, action := range []string{"pause", "heartbeat", "resume"} {
		response = performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+fixture.sessionID.String()+"/"+action, fixture.studentToken, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("student lifecycle %s = %d: %s", action, response.Code, response.Body.String())
		}
		assertStudentPayloadHasNoPrivateFields(t, response.Body.Bytes())
		if strings.Contains(response.Body.String(), fixture.privateCanary) {
			t.Fatalf("student lifecycle %s leaked private answer canary: %s", action, response.Body.String())
		}
	}
	pauseEvent := <-studentEvents
	assertStudentPayloadHasNoPrivateFields(t, pauseEvent)
	if strings.Contains(string(pauseEvent), fixture.privateCanary) {
		t.Fatalf("student lifecycle event leaked private answer canary: %s", pauseEvent)
	}

	response = performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+fixture.sessionID.String()+"/support", fixture.studentToken, map[string]any{"type": "HINT"})
	if response.Code != http.StatusOK {
		t.Fatalf("student support = %d: %s", response.Code, response.Body.String())
	}
	assertStudentPayloadHasNoPrivateFields(t, response.Body.Bytes())
	if strings.Contains(response.Body.String(), fixture.privateCanary) {
		t.Fatalf("student support leaked private answer canary: %s", response.Body.String())
	}

	response = performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+fixture.sessionID.String()+"/answers", fixture.studentToken, map[string]any{"answer": fixture.privateCanary})
	if response.Code != http.StatusOK {
		t.Fatalf("student answer = %d: %s", response.Code, response.Body.String())
	}
	assertStudentPayloadHasNoPrivateFields(t, response.Body.Bytes())
	if strings.Contains(response.Body.String(), fixture.privateCanary) {
		t.Fatalf("student answer response echoed private answer: %s", response.Body.String())
	}
	for {
		select {
		case event := <-studentEvents:
			assertStudentPayloadHasNoPrivateFields(t, event)
			if strings.Contains(string(event), fixture.privateCanary) {
				t.Fatalf("student realtime event leaked private answer canary: %s", event)
			}
		default:
			goto realtimeDrained
		}
	}

realtimeDrained:
	rows, err := pool.Query(ctx, `SELECT student_payload_json FROM tutor_events WHERE session_id=$1 ORDER BY sequence`, fixture.sessionID)
	if err != nil {
		t.Fatalf("load persisted student events: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var payload json.RawMessage
		if err := rows.Scan(&payload); err != nil {
			t.Fatalf("scan persisted student event: %v", err)
		}
		assertStudentPayloadHasNoPrivateFields(t, payload)
		if strings.Contains(string(payload), fixture.privateCanary) {
			t.Fatalf("persisted student event leaked private answer canary: %s", payload)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate persisted student events: %v", err)
	}

	response = performParentSessionRequest(router, fixture.parentToken, fixture.studentID, fixture.sessionID)
	if response.Code != http.StatusOK {
		t.Fatalf("parent live session status = %d: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), fixture.privateCanary) {
		t.Fatalf("authorized parent did not receive private supervision data: %s", response.Body.String())
	}
	response = performParentSessionRequest(router, fixture.studentToken, fixture.studentID, fixture.sessionID)
	if response.Code != http.StatusForbidden {
		t.Fatalf("student calling parent endpoint status = %d, want %d", response.Code, http.StatusForbidden)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/parent/child/"+fixture.studentID.String()+"/send-answer", strings.NewReader(`{"answer":"10"}`))
	request.Header.Set("Authorization", "Bearer "+fixture.parentToken)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("SendAnswerToStudent-like route exists with status %d", response.Code)
	}
}

type securityFixture struct {
	studentToken       string
	parentToken        string
	releasedQuestionID uuid.UUID
	draftQuestionID    uuid.UUID
	privateCanary      string
	studentID          uuid.UUID
	parentUserID       uuid.UUID
	sessionID          uuid.UUID
}

func seedSecurityFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) securityFixture {
	t.Helper()

	studentUserID := uuid.New()
	studentID := uuid.New()
	parentUserID := uuid.New()
	studentToken := "student-" + uuid.NewString()
	parentToken := "parent-" + uuid.NewString()
	studentHash := sha256.Sum256([]byte(studentToken))
	parentHash := sha256.Sum256([]byte(parentToken))

	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO users (id, role_code, display_name) VALUES ($1, 'STUDENT', '测试学生'), ($2, 'PARENT', '测试家长')`, studentUserID, parentUserID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO students (id, user_id, grade_level) VALUES ($1, $2, 7)`, studentID, studentUserID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO parent_student_links (parent_user_id, student_id) VALUES ($1, $2)`, parentUserID, studentID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO sessions (id, user_id, token_hash, expires_at) VALUES ($1, $2, $3, $4), ($5, $6, $7, $4)`,
			uuid.New(), studentUserID, studentHash[:], time.Now().Add(time.Hour),
			uuid.New(), parentUserID, parentHash[:],
		)
		return err
	})
	if err != nil {
		t.Fatalf("seed identities: %v", err)
	}

	domainID := uuid.New()
	unitID := uuid.New()
	knowledgePointID := uuid.New()
	releasedQuestionID := uuid.New()
	draftQuestionID := uuid.New()
	privateCanary := "PRIVATE_ANSWER_10_FIXED_COST_CANARY"
	sessionID := uuid.New()
	studentAnswerID := uuid.New()

	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO domains (id, subject_id, code, name) SELECT $1, id, 'EQUATION', '方程' FROM subjects WHERE code = 'MATH'`, domainID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO units (id, domain_id, code, name, grade_band_code) VALUES ($1, $2, 'LINEAR', '一元一次方程', 'JUNIOR_SECONDARY')`, unitID, domainID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO knowledge_points (id, subject_id, domain_id, unit_id, grade_band_code, code, name, default_difficulty, status, curriculum_version)
SELECT $1, s.id, $2, $3, 'JUNIOR_SECONDARY', 'MATH-EQ-FIXED-COST', '固定费用方程', 'L2', 'RELEASED', 'v1'
FROM subjects s WHERE s.code = 'MATH'`, knowledgePointID, domainID, unitID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO questions (id, knowledge_point_id, difficulty, question_type, prompt_public, scene_public_json, input_schema_json, status, content_version)
VALUES ($1, $3, 'L2', 'FREE_TEXT', '3杯同价饮料加6元配送费共36元，每杯多少钱？', '{"kind":"SHOPPING"}', '{"type":"string"}', 'DRAFT', 'v1'),
       ($2, $3, 'L2', 'FREE_TEXT', '未发布题目', '{}', '{"type":"string"}', 'DRAFT', 'v1')`, releasedQuestionID, draftQuestionID, knowledgePointID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
INSERT INTO question_private_answers (question_id, correct_answer_json, full_solution_private, teacher_reference_answer, scoring_key_json, misconceptions_private_json, hint_policy_private_json)
VALUES ($1, jsonb_build_object('value', $3::text), $3, $3, '{"exact":true}', '["FIXED_COST_IGNORED"]', '{"max_hints":2}'),
       ($2, '{"value":"hidden"}', 'hidden', 'hidden', '{}', '[]', '{}')`, releasedQuestionID, draftQuestionID, privateCanary)
		if err != nil {
			return err
		}
		sourceID, versionID, validationID, reviewID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
		if _, err := tx.Exec(ctx, `INSERT INTO content_sources (id,name,source_type,license_code) VALUES ($1,'integration fixture','INTERNAL_RULE','INTERNAL')`, sourceID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO content_versions (id,question_id,version,schema_version,generator_provider,generator_model,source_id,asset_json) VALUES ($1,$2,'v1','content-question-v1','fixture','fixture-generator',$3,'{}')`, versionID, releasedQuestionID, sourceID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO content_validations (id,question_id,content_version,schema_version,status,checks_json,validator_version) VALUES ($1,$2,'v1','content-question-v1','PASS','[]','fixture-v1')`, validationID, releasedQuestionID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE questions SET status='AUTOMATIC_VALIDATED' WHERE id=$1`, releasedQuestionID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO content_reviews (id,question_id,content_version,schema_version,result,reviewer_provider,reviewer_model,findings_json) VALUES ($1,$2,'v1','content-question-v1','PASS','fixture-secondary','fixture-reviewer','[]')`, reviewID, releasedQuestionID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE questions SET status='AI_REVIEWED' WHERE id=$1`, releasedQuestionID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE questions SET status='RELEASED' WHERE id=$1`, releasedQuestionID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO content_release_records (id,question_id,from_status,to_status,validation_id,review_id,reason) VALUES ($1,$2,'AI_REVIEWED','RELEASED',$3,$4,'integration fixture release')`, uuid.New(), releasedQuestionID, validationID, reviewID); err != nil {
			return err
		}
		var subjectID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM subjects WHERE code = 'MATH'`).Scan(&subjectID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO learning_sessions (id, student_id, subject_id, current_question_id, status, target_minutes, current_state, socratic_fail_count, assistance_level, last_resumed_at, last_activity_at)
VALUES ($1, $2, $3, $4, 'ACTIVE', 20, 'PROBE', 2, 1, now(), now())`, sessionID, studentID, subjectID, releasedQuestionID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO tutor_turns (id, session_id, sequence, actor, action, message, reason_private)
VALUES ($1, $2, 4, 'TUTOR', 'PROBE', '配送费是哪一部分？', '检查固定费用识别')`, uuid.New(), sessionID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO student_answers (id, session_id, question_id, answer_text) VALUES ($1, $2, $3, '12')`, studentAnswerID, sessionID, releasedQuestionID); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
INSERT INTO answer_analyses (id, student_answer_id, answer_correct, reasoning_quality, confidence, error_type, misconceptions_private_json, emotion_signal, engagement, recommended_action)
VALUES ($1, $2, false, 'PARTIAL', 0.91, 'FIXED_COST_IGNORED', '["EQ-WORD-FIXED-COST"]', 'NEUTRAL', 'NORMAL', 'PROBE')`, uuid.New(), studentAnswerID)
		return err
	})
	if err != nil {
		t.Fatalf("seed curriculum and questions: %v", err)
	}

	return securityFixture{
		studentToken:       studentToken,
		parentToken:        parentToken,
		releasedQuestionID: releasedQuestionID,
		draftQuestionID:    draftQuestionID,
		privateCanary:      privateCanary,
		studentID:          studentID,
		parentUserID:       parentUserID,
		sessionID:          sessionID,
	}
}

func assertParentStudentBinding(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fixture securityFixture) {
	t.Helper()
	repository := parentrepo.NewRepository(pool)
	allowed, err := repository.CanSupervise(ctx, fixture.parentUserID, fixture.studentID)
	if err != nil {
		t.Fatalf("check parent-student binding: %v", err)
	}
	if !allowed {
		t.Fatal("active parent-student binding was not recognized")
	}

	allowed, err = repository.CanSupervise(ctx, uuid.New(), fixture.studentID)
	if err != nil {
		t.Fatalf("check unrelated parent: %v", err)
	}
	if allowed {
		t.Fatal("unrelated parent was allowed to supervise student")
	}
}

func assertFiveSubjectsSeeded(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT code FROM subjects ORDER BY sort_order`)
	if err != nil {
		t.Fatalf("query subjects: %v", err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			t.Fatalf("scan subject: %v", err)
		}
		got = append(got, code)
	}
	want := []string{"MATH", "CHINESE", "ENGLISH", "PHYSICS", "CHEMISTRY"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("seeded subjects = %v, want %v", got, want)
	}
}

func assertPrivateAnswerExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, questionID uuid.UUID) {
	t.Helper()
	var solution string
	if err := pool.QueryRow(ctx, `SELECT full_solution_private FROM question_private_answers WHERE question_id = $1`, questionID).Scan(&solution); err != nil {
		t.Fatalf("private answer was not stored: %v", err)
	}
	if solution == "" {
		t.Fatal("private answer is empty")
	}
}

func performQuestionRequest(handler http.Handler, token string, questionID uuid.UUID) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/student/questions/"+questionID.String(), nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func performParentSessionRequest(handler http.Handler, token string, studentID, sessionID uuid.UUID) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/parent/child/"+studentID.String()+"/session/"+sessionID.String(), nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertStudentPayloadHasNoPrivateFields(t *testing.T, body []byte) {
	t.Helper()
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode student response: %v", err)
	}
	forbidden := map[string]bool{
		"correct_answer": true, "correct_answer_json": true, "answer_correct": true,
		"full_solution": true, "full_solution_private": true,
		"teacher_reference_answer": true, "teacher_solution": true,
		"scoring_key": true, "scoring_key_json": true,
		"misconceptions": true, "misconceptions_private_json": true,
		"hint_policy": true, "hint_policy_private_json": true,
		"reason_private": true,
	}
	var walk func(any)
	walk = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			for key, child := range typed {
				if forbidden[strings.ToLower(key)] {
					t.Fatalf("student response contains forbidden field %q", key)
				}
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(payload)
}
