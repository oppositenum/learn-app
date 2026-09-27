package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oppositenum/ai-learning-tutor/server/internal/api"
	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
	"github.com/oppositenum/ai-learning-tutor/server/internal/classroom"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/internal/parent"
	"github.com/oppositenum/ai-learning-tutor/server/internal/planner"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

// M14: the parent settings page adjusts the plan only. It can lean the plan
// toward a knowledge domain and record "today is not a good day"; it cannot
// name a question, change an answer, send an answer or submit for the child.
// Failure messages name fields, enums, dates and subject codes only.

type microInterventionFixture struct {
	pool     *pgxpool.Pool
	router   http.Handler
	security securityFixture
	path     string
}

func newMicroInterventionFixture(t *testing.T) microInterventionFixture {
	t.Helper()
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	security := seedSecurityFixture(t, ctx, pool)
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET status='ABANDONED',ended_at=now() WHERE id=$1`, security.sessionID); err != nil {
		t.Fatal(err)
	}
	plannerService := planner.NewService(pool)
	service := classroom.NewService(pool, nil, nil, nil, plannerService)
	router := api.NewRouter(api.Dependencies{Authenticate: auth.NewSessionAuthenticator(pool).Middleware, Classroom: classroom.NewHandler(service, pool, parent.NewRepository(pool), plannerService)})
	return microInterventionFixture{pool: pool, router: router, security: security, path: "/api/v1/parent/child/" + security.studentID.String() + "/preferences"}
}

func (fixture microInterventionFixture) date(t *testing.T, expression string) string {
	t.Helper()
	var value string
	if err := fixture.pool.QueryRow(context.Background(), `SELECT (`+expression+`)::text`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func (fixture microInterventionFixture) save(t *testing.T, body map[string]any) preferenceUpdateResponse {
	t.Helper()
	response := performJSON(fixture.router, http.MethodPut, fixture.path, fixture.security.parentToken, body)
	if response.Code != http.StatusOK {
		t.Fatalf("preferences=%d", response.Code)
	}
	return decodePreferenceUpdate(t, response)
}

func (fixture microInterventionFixture) stored(t *testing.T) (int, bool, bool, []string, *uuid.UUID) {
	t.Helper()
	var minutes int
	var reviewOnly, reduceIntensity bool
	var priorities []string
	var domainID *uuid.UUID
	if err := fixture.pool.QueryRow(context.Background(), `SELECT daily_minutes,review_only,reduce_intensity,priority_subject_codes,priority_domain_id FROM parent_preferences WHERE student_id=$1`, fixture.security.studentID).Scan(&minutes, &reviewOnly, &reduceIntensity, &priorities, &domainID); err != nil {
		t.Fatal(err)
	}
	return minutes, reviewOnly, reduceIntensity, priorities, domainID
}

// todayCard is what the child's today page builds its cards and unlocks from.
type todayCard struct {
	ID               uuid.UUID `json:"id"`
	Subject          string    `json:"subject"`
	KnowledgePointID string    `json:"knowledge_point_id"`
	Minutes          int       `json:"minutes"`
	Mode             string    `json:"mode"`
	Status           string    `json:"status"`
	SessionStatus    string    `json:"session_status"`
}

func (fixture microInterventionFixture) studentToday(t *testing.T) (string, []todayCard) {
	t.Helper()
	response := performJSON(fixture.router, http.MethodGet, "/api/v1/student/today", fixture.security.studentToken, nil)
	var payload struct {
		LearningDate string `json:"learning_date"`
		Plans        []struct {
			Blocks []todayCard `json:"blocks"`
		} `json:"plans"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &payload) != nil || len(payload.Plans) != 1 {
		t.Fatalf("student today=%d plans=%d", response.Code, len(payload.Plans))
	}
	return payload.LearningDate, payload.Plans[0].Blocks
}

func basePreferences() map[string]any {
	return map[string]any{"daily_minutes": 30, "priority_subject_codes": []string{}, "review_only": false, "reduce_intensity": false}
}

func withPreference(key string, value any) map[string]any {
	body := basePreferences()
	body[key] = value
	return body
}

func TestParentPlanMicroInterventionUpdatesTodayWithoutAClassroom(t *testing.T) {
	fixture := newMicroInterventionFixture(t)
	today := fixture.date(t, "current_date")
	fixture.studentToday(t)

	for name, body := range map[string]map[string]any{
		"daily_minutes":          withPreference("daily_minutes", 45),
		"priority_subject_codes": withPreference("priority_subject_codes", []string{"ENGLISH"}),
		"review_only":            withPreference("review_only", true),
		"reduce_intensity":       withPreference("reduce_intensity", true),
	} {
		update := fixture.save(t, body)
		if !update.Saved || !update.PlanUpdated || update.TodayPreserved || update.AppliesFrom != today || update.AnswerControlsAvailable {
			t.Fatalf("%s: update=%+v want applies_from=%s", name, update, today)
		}
	}

	// "Today is not a good day" records both reduce_intensity and review_only,
	// and today's plan follows them at once.
	update := fixture.save(t, withPreference("state_not_good", true))
	if !update.PlanUpdated || update.TodayPreserved || update.AppliesFrom != today {
		t.Fatalf("state_not_good update=%+v want applies_from=%s", update, today)
	}
	minutes, reviewOnly, reduceIntensity, _, _ := fixture.stored(t)
	if minutes != 30 || !reviewOnly || !reduceIntensity {
		t.Fatalf("state_not_good stored minutes=%d review_only=%t reduce_intensity=%t", minutes, reviewOnly, reduceIntensity)
	}
	var target, reviewBlocks, blocks int
	if err := fixture.pool.QueryRow(context.Background(), `
SELECT p.target_minutes,count(b.id) FILTER(WHERE b.mode='REVIEW'),count(b.id)
FROM learning_plans p JOIN learning_plan_blocks b ON b.plan_id=p.id
WHERE p.student_id=$1 AND p.plan_date=current_date AND p.status='PROPOSED' GROUP BY p.target_minutes`, fixture.security.studentID).Scan(&target, &reviewBlocks, &blocks); err != nil {
		t.Fatal(err)
	}
	if target != 20 || reviewBlocks != blocks || blocks == 0 {
		t.Fatalf("state_not_good plan target=%d review_blocks=%d blocks=%d", target, reviewBlocks, blocks)
	}
	read := performJSON(fixture.router, http.MethodGet, fixture.path, fixture.security.parentToken, nil)
	var saved struct {
		ReviewOnly      bool `json:"review_only"`
		ReduceIntensity bool `json:"reduce_intensity"`
	}
	if read.Code != http.StatusOK || json.Unmarshal(read.Body.Bytes(), &saved) != nil || !saved.ReviewOnly || !saved.ReduceIntensity {
		t.Fatalf("state_not_good read=%d review_only=%t reduce_intensity=%t", read.Code, saved.ReviewOnly, saved.ReduceIntensity)
	}
}

func TestParentPlanMicroInterventionKeepsTodayWhileAClassroomIsOpen(t *testing.T) {
	fixture := newMicroInterventionFixture(t)
	today := fixture.date(t, "current_date")
	tomorrow := fixture.date(t, "current_date+1")
	learningDate, before := fixture.studentToday(t)
	if learningDate != today || len(before) == 0 {
		t.Fatalf("student today learning_date=%s want %s blocks=%d", learningDate, today, len(before))
	}
	started := performJSON(fixture.router, http.MethodPost, "/api/v1/student/sessions", fixture.security.studentToken, map[string]any{"plan_block_id": before[0].ID})
	var session classroom.StudentSession
	if started.Code != http.StatusOK || json.Unmarshal(started.Body.Bytes(), &session) != nil {
		t.Fatalf("start=%d", started.Code)
	}
	_, opened := fixture.studentToday(t)

	// Saved in this order; the last one decides the effective-date plan.
	for _, step := range []struct {
		name string
		body map[string]any
	}{
		{"daily_minutes", withPreference("daily_minutes", 50)},
		{"priority_subject_codes", withPreference("priority_subject_codes", []string{"PHYSICS"})},
		{"review_only", withPreference("review_only", true)},
		{"reduce_intensity", withPreference("reduce_intensity", true)},
		{"state_not_good", withPreference("state_not_good", true)},
	} {
		name, body := step.name, step.body
		update := fixture.save(t, body)
		if !update.Saved || !update.TodayPreserved || update.AppliesFrom != tomorrow || update.AnswerControlsAvailable {
			t.Fatalf("%s: update=%+v want today_preserved and applies_from=%s", name, update, tomorrow)
		}
		// Today's cards, their minutes, status and unlocks stay as they were.
		_, after := fixture.studentToday(t)
		if len(after) != len(opened) {
			t.Fatalf("%s: today blocks=%d want %d", name, len(after), len(opened))
		}
		for index := range opened {
			if after[index] != opened[index] {
				t.Fatalf("%s: today block %d changed subject=%s->%s minutes=%d->%d mode=%s->%s status=%s->%s session_status=%s->%s id_same=%t", name, index, opened[index].Subject, after[index].Subject, opened[index].Minutes, after[index].Minutes, opened[index].Mode, after[index].Mode, opened[index].Status, after[index].Status, opened[index].SessionStatus, after[index].SessionStatus, opened[index].ID == after[index].ID)
			}
		}
	}
	var status string
	if err := fixture.pool.QueryRow(context.Background(), `SELECT status FROM learning_sessions WHERE id=$1`, session.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "ACTIVE" {
		t.Fatalf("classroom status=%s want ACTIVE", status)
	}
	// The plan built for the effective date carries the new preferences.
	tomorrowDay, err := time.Parse("2006-01-02", tomorrow)
	if err != nil {
		t.Fatal(err)
	}
	next, err := planner.NewService(fixture.pool).Ensure(context.Background(), fixture.security.studentID, tomorrowDay, uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	reviewBlocks := 0
	for _, block := range next.Blocks {
		if block.Mode == planner.ModeReview {
			reviewBlocks++
		}
	}
	if next.TargetMinutes != 20 || len(next.Blocks) == 0 || reviewBlocks != len(next.Blocks) {
		t.Fatalf("effective-date plan target=%d review_blocks=%d blocks=%d", next.TargetMinutes, reviewBlocks, len(next.Blocks))
	}
}

func TestParentPlanMicroInterventionPriorityDomainBelongsToAnEnabledSubject(t *testing.T) {
	fixture := newMicroInterventionFixture(t)
	ctx := context.Background()
	today := fixture.date(t, "current_date")

	read := performJSON(fixture.router, http.MethodGet, fixture.path, fixture.security.parentToken, nil)
	var options struct {
		PriorityDomainID *string `json:"priority_domain_id"`
		DomainOptions    []struct {
			ID          string `json:"id"`
			SubjectCode string `json:"subject_code"`
			Name        string `json:"name"`
		} `json:"domain_options"`
	}
	if read.Code != http.StatusOK || json.Unmarshal(read.Body.Bytes(), &options) != nil || options.PriorityDomainID != nil || len(options.DomainOptions) == 0 {
		t.Fatalf("preferences read=%d options=%d", read.Code, len(options.DomainOptions))
	}
	for _, option := range options.DomainOptions {
		if _, err := uuid.Parse(option.ID); err != nil || !map[string]bool{"MATH": true, "CHINESE": true, "ENGLISH": true, "PHYSICS": true, "CHEMISTRY": true}[option.SubjectCode] || option.Name == "" {
			t.Fatalf("domain option subject_code=%s id_valid=%t name_present=%t", option.SubjectCode, err == nil, option.Name != "")
		}
	}

	// The domain of the released question the child would practise.
	var mathDomain, englishDomain uuid.UUID
	if err := fixture.pool.QueryRow(ctx, `SELECT kp.domain_id FROM questions q JOIN knowledge_points kp ON kp.id=q.knowledge_point_id WHERE q.id=$1`, fixture.security.releasedQuestionID).Scan(&mathDomain); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(ctx, `SELECT d.id FROM domains d JOIN subjects s ON s.id=d.subject_id WHERE s.code='ENGLISH' ORDER BY d.sort_order LIMIT 1`).Scan(&englishDomain); err != nil {
		t.Fatal(err)
	}

	mathOnly := withPreference("enabled_subject_codes", []string{"MATH"})
	mathOnly["priority_domain_id"] = mathDomain.String()
	update := fixture.save(t, mathOnly)
	if !update.PlanUpdated || update.TodayPreserved || update.AppliesFrom != today {
		t.Fatalf("priority domain update=%+v want applies_from=%s", update, today)
	}
	_, _, _, _, storedDomain := fixture.stored(t)
	if storedDomain == nil || *storedDomain != mathDomain {
		t.Fatal("priority_domain_id not stored")
	}
	var inDomain bool
	if err := fixture.pool.QueryRow(ctx, `
SELECT bool_and(kp.domain_id=$2) FROM learning_plans p
JOIN learning_plan_blocks b ON b.plan_id=p.id JOIN knowledge_points kp ON kp.id=b.knowledge_point_id
WHERE p.student_id=$1 AND p.plan_date=current_date AND p.status='PROPOSED'`, fixture.security.studentID, mathDomain).Scan(&inDomain); err != nil {
		t.Fatal(err)
	}
	if !inDomain {
		t.Fatal("today's math block is outside the priority domain")
	}
	read = performJSON(fixture.router, http.MethodGet, fixture.path, fixture.security.parentToken, nil)
	if json.Unmarshal(read.Body.Bytes(), &options) != nil || options.PriorityDomainID == nil || *options.PriorityDomainID != mathDomain.String() {
		t.Fatal("priority_domain_id not read back")
	}

	// A domain of a subject the parent did not tick, a question id, an unknown
	// id and a non-id are all refused, and the saved domain stays.
	for name, value := range map[string]string{
		"disabled_subject_domain": englishDomain.String(),
		"question_id":             fixture.security.releasedQuestionID.String(),
		"unknown_id":              uuid.NewString(),
		"not_an_id":               "EQUATION",
	} {
		body := withPreference("enabled_subject_codes", []string{"MATH"})
		body["priority_domain_id"] = value
		if response := performJSON(fixture.router, http.MethodPut, fixture.path, fixture.security.parentToken, body); response.Code != http.StatusBadRequest {
			t.Fatalf("%s accepted as priority_domain_id: %d", name, response.Code)
		}
	}
	_, _, _, _, storedDomain = fixture.stored(t)
	if storedDomain == nil || *storedDomain != mathDomain {
		t.Fatal("a refused priority_domain_id changed the stored domain")
	}

	// With every subject enabled the English domain is accepted; an empty
	// value clears the choice.
	allSubjects := withPreference("priority_domain_id", englishDomain.String())
	fixture.save(t, allSubjects)
	if _, _, _, _, storedDomain = fixture.stored(t); storedDomain == nil || *storedDomain != englishDomain {
		t.Fatal("english priority_domain_id not stored with all subjects enabled")
	}
	fixture.save(t, withPreference("priority_domain_id", ""))
	if _, _, _, _, storedDomain = fixture.stored(t); storedDomain != nil {
		t.Fatal("empty priority_domain_id did not clear the domain")
	}
}

func TestParentPlanMicroInterventionRefusesQuestionAndAnswerFields(t *testing.T) {
	fixture := newMicroInterventionFixture(t)
	fixture.save(t, withPreference("daily_minutes", 25))

	for _, field := range []string{
		"question_id", "question_ids", "knowledge_point_id",
		"correct_answer", "answer", "answer_override", "standard_answer",
		"send_answer_to_student", "send_answer", "reveal_answer",
		"submit_for_student", "student_answer", "answer_text",
	} {
		body := withPreference("daily_minutes", 55)
		body[field] = fixture.security.releasedQuestionID.String()
		if response := performJSON(fixture.router, http.MethodPut, fixture.path, fixture.security.parentToken, body); response.Code != http.StatusBadRequest {
			t.Fatalf("field %s accepted: %d", field, response.Code)
		}
	}
	if minutes, _, _, _, _ := fixture.stored(t); minutes != 25 {
		t.Fatalf("a refused request changed daily_minutes to %d", minutes)
	}
	for _, path := range []string{
		"/api/v1/parent/child/" + fixture.security.studentID.String() + "/send-answer",
		"/api/v1/parent/child/" + fixture.security.studentID.String() + "/answers",
		"/api/v1/parent/child/" + fixture.security.studentID.String() + "/submit",
	} {
		if response := performJSON(fixture.router, http.MethodPost, path, fixture.security.parentToken, map[string]any{"question_id": fixture.security.releasedQuestionID.String()}); response.Code != http.StatusNotFound && response.Code != http.StatusMethodNotAllowed {
			t.Fatalf("parent answer route exists: %d", response.Code)
		}
	}
}
