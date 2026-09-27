package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// The parent live classroom shows the question, the standard answer, the
// child's short answer, the hint count and a gentle emotion label only while
// the classroom is running. Failure messages name fields, enums and numbers
// only; they never print the question, the answers or the tutor's words.

type parentLivePrivate struct {
	prompt       string
	solution     string
	childAnswer  string
	tutorMessage string
}

func (fixture parentOverviewFixture) livePrivate(t *testing.T) parentLivePrivate {
	t.Helper()
	// A distinctive short answer so a body check cannot match a timestamp.
	fixture.exec(t, `UPDATE student_answers SET answer_text='CHILD_SHORT_ANSWER_CANARY' WHERE session_id=$1`, fixture.security.sessionID)
	var private parentLivePrivate
	err := fixture.pool.QueryRow(context.Background(), `
SELECT q.prompt_public, qa.full_solution_private,
       (SELECT answer_text FROM student_answers WHERE session_id=$2 ORDER BY submitted_at DESC LIMIT 1),
       (SELECT message FROM tutor_turns WHERE session_id=$2 AND actor='TUTOR' ORDER BY sequence DESC LIMIT 1)
FROM questions q JOIN question_private_answers qa ON qa.question_id=q.id WHERE q.id=$1`,
		fixture.security.releasedQuestionID, fixture.security.sessionID).Scan(&private.prompt, &private.solution, &private.childAnswer, &private.tutorMessage)
	if err != nil {
		t.Fatal(err)
	}
	return private
}

func (fixture parentOverviewFixture) parentLive(t *testing.T) (map[string]any, string) {
	t.Helper()
	response := performParentSessionRequest(fixture.router, fixture.security.parentToken, fixture.security.studentID, fixture.security.sessionID)
	if response.Code != http.StatusOK {
		t.Fatalf("parent live=%d", response.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return payload, response.Body.String()
}

// assertStudentSideClean reads the child's classroom APIs at the same moment
// and requires none of the parent-only answer fields.
func (fixture parentOverviewFixture) assertStudentSideClean(t *testing.T) {
	t.Helper()
	fixture.assertStudentAPIsStayPrivate(t)
	for _, path := range []string{"/api/v1/student/sessions/current", "/api/v1/student/sessions/" + fixture.security.sessionID.String()} {
		response := performJSON(fixture.router, http.MethodGet, path, fixture.security.studentToken, nil)
		for _, key := range []string{`"correct_answer"`, `"full_solution"`, `"student_answer_preview"`, `"student_answer_visibility"`, `"hint_count"`} {
			if strings.Contains(response.Body.String(), key) {
				t.Fatalf("%s carries parent field %s", path, key)
			}
		}
	}
}

func assertLiveNumber(t *testing.T, payload map[string]any, key string, want float64) {
	t.Helper()
	if got, ok := payload[key].(float64); !ok || got != want {
		t.Fatalf("%s=%v want %v", key, payload[key], want)
	}
}

func assertLiveString(t *testing.T, payload map[string]any, key, want string) {
	t.Helper()
	if got, _ := payload[key].(string); got != want {
		t.Fatalf("%s=%v want %s", key, payload[key], want)
	}
}

func TestParentLiveClassroomShowsTheQuestionHintsAndEmotionWhileActive(t *testing.T) {
	fixture := newParentOverviewFixture(t)
	security := fixture.security
	private := fixture.livePrivate(t)

	payload, body := fixture.parentLive(t)
	assertLiveString(t, payload, "status", "ACTIVE")
	assertLiveString(t, payload, "detail_mode", "LIVE")
	if payload["question_prompt"] != private.prompt {
		t.Fatal("question_prompt is not the current question")
	}
	if payload["correct_answer"] == nil || payload["full_solution"] != private.solution || !strings.Contains(body, security.privateCanary) {
		t.Fatal("correct_answer or full_solution missing while active")
	}
	assertLiveString(t, payload, "student_answer_visibility", "SHORT_CURRENT")
	if payload["student_answer_preview"] != private.childAnswer {
		t.Fatal("student_answer_preview is not the current short answer")
	}
	assertLiveNumber(t, payload, "socratic_round", 2)
	assertLiveString(t, payload, "tutor_action", "PROBE")
	if !strings.Contains(body, private.tutorMessage) {
		t.Fatal("tutor turn missing while active")
	}
	// The Socratic follow-up after a wrong answer is not a hint.
	assertLiveNumber(t, payload, "hint_count", 0)
	assertLiveString(t, payload, "emotion", "CALM")
	fixture.assertStudentSideClean(t)

	// Every hint, scaffold, analogy, backtrack and explanation counts once;
	// more follow-ups do not.
	fixture.exec(t, `
INSERT INTO tutor_turns(id,session_id,sequence,actor,action,message,reason_private)
VALUES($1,$6,5,'TUTOR','HINT','h','r'),($2,$6,6,'TUTOR','PROBE','p','r'),($3,$6,7,'TUTOR','SCAFFOLD','s','r'),
      ($4,$6,8,'TUTOR','HINT','h','r'),($5,$6,9,'TUTOR','EXPLAIN','e','r')`,
		uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), security.sessionID)
	payload, _ = fixture.parentLive(t)
	assertLiveNumber(t, payload, "hint_count", 4)
	assertLiveString(t, payload, "tutor_action", "EXPLAIN")
	assertLiveNumber(t, payload, "socratic_round", 2)

	for signal, want := range map[string]string{"FRUSTRATED": "FRUSTRATED", "BORED": "BORED", "ANXIOUS": "CALM", "NEUTRAL": "CALM"} {
		fixture.exec(t, `UPDATE answer_analyses SET emotion_signal=$2 WHERE student_answer_id IN (SELECT id FROM student_answers WHERE session_id=$1)`, security.sessionID, signal)
		payload, _ = fixture.parentLive(t)
		assertLiveString(t, payload, "emotion", want)
	}
	// No analysis yet reads as calm.
	fixture.exec(t, `DELETE FROM answer_analyses WHERE student_answer_id IN (SELECT id FROM student_answers WHERE session_id=$1)`, security.sessionID)
	payload, _ = fixture.parentLive(t)
	assertLiveString(t, payload, "emotion", "CALM")

	// The short answer preview keeps its 80-character, single-line rule.
	for _, test := range []struct {
		answer     string
		visibility string
		preview    bool
	}{
		{strings.Repeat("界", 80), "SHORT_CURRENT", true},
		{strings.Repeat("界", 81), "WITHHELD_LONG", false},
		{"a\nb", "WITHHELD_LONG", false},
		{"", "NONE", false},
	} {
		fixture.exec(t, `UPDATE student_answers SET answer_text=$2 WHERE session_id=$1`, security.sessionID, test.answer)
		payload, _ = fixture.parentLive(t)
		assertLiveString(t, payload, "student_answer_visibility", test.visibility)
		if _, present := payload["student_answer_preview"]; present != test.preview {
			t.Fatalf("student_answer_preview present=%t want %t for visibility %s", present, test.preview, test.visibility)
		}
		fixture.assertStudentSideClean(t)
	}
}

func assertParentLiveWithheld(t *testing.T, fixture parentOverviewFixture, private parentLivePrivate) map[string]any {
	t.Helper()
	payload, body := fixture.parentLive(t)
	assertLiveString(t, payload, "status", "PAUSED")
	assertLiveString(t, payload, "detail_mode", "REPORT")
	assertLiveString(t, payload, "student_answer_visibility", "WITHHELD_NOT_ACTIVE")
	if _, present := payload["student_answer_preview"]; present {
		t.Fatal("student_answer_preview present while paused")
	}
	if payload["question_prompt"] != "" || payload["correct_answer"] != nil || payload["full_solution"] != "" {
		t.Fatal("question_prompt, correct_answer or full_solution present while paused")
	}
	for name, text := range map[string]string{"question": private.prompt, "solution": private.solution, "canary": fixture.security.privateCanary, "child answer": private.childAnswer, "tutor turn": private.tutorMessage} {
		if strings.Contains(body, text) {
			t.Fatalf("paused response carries the %s", name)
		}
	}
	// What the parent still sees: round, action, hints and emotion.
	assertLiveNumber(t, payload, "socratic_round", 2)
	assertLiveString(t, payload, "tutor_action", "PROBE")
	assertLiveNumber(t, payload, "hint_count", 0)
	assertLiveString(t, payload, "emotion", "CALM")
	return payload
}

func TestParentLiveClassroomWithholdsTheQuestionAndAnswersWhenPaused(t *testing.T) {
	fixture := newParentOverviewFixture(t)
	security := fixture.security
	private := fixture.livePrivate(t)

	pause := performJSON(fixture.router, http.MethodPost, "/api/v1/student/sessions/"+security.sessionID.String()+"/pause", security.studentToken, map[string]any{})
	if pause.Code != http.StatusOK {
		t.Fatalf("pause=%d", pause.Code)
	}
	assertParentLiveWithheld(t, fixture, private)
	fixture.assertStudentSideClean(t)

	// Resuming brings the live view back.
	resume := performJSON(fixture.router, http.MethodPost, "/api/v1/student/sessions/"+security.sessionID.String()+"/resume", security.studentToken, map[string]any{})
	if resume.Code != http.StatusOK {
		t.Fatalf("resume=%d", resume.Code)
	}
	payload, _ := fixture.parentLive(t)
	assertLiveString(t, payload, "status", "ACTIVE")
	if payload["question_prompt"] != private.prompt || payload["correct_answer"] == nil {
		t.Fatal("question_prompt or correct_answer missing after resume")
	}
}

func TestParentLiveClassroomTreatsNinetyQuietSecondsAsPaused(t *testing.T) {
	fixture := newParentOverviewFixture(t)
	private := fixture.livePrivate(t)
	// Sixty quiet seconds is still a running classroom.
	fixture.exec(t, `UPDATE learning_sessions SET last_activity_at=CURRENT_TIMESTAMP-interval '60 seconds' WHERE id=$1`, fixture.security.sessionID)
	payload, _ := fixture.parentLive(t)
	assertLiveString(t, payload, "status", "ACTIVE")
	assertLiveString(t, payload, "student_answer_visibility", "SHORT_CURRENT")

	// Past ninety seconds the parent reads it as paused before any recovery
	// has run; the child's classroom read then pauses it for real.
	fixture.exec(t, `UPDATE learning_sessions SET last_activity_at=CURRENT_TIMESTAMP-interval '91 seconds' WHERE id=$1`, fixture.security.sessionID)
	assertParentLiveWithheld(t, fixture, private)
	fixture.assertStudentSideClean(t)
	assertParentLiveWithheld(t, fixture, private)
}

func TestParentLiveClassroomKeepsTheCompletedReport(t *testing.T) {
	fixture := newParentOverviewFixture(t)
	private := fixture.livePrivate(t)
	fixture.exec(t, `UPDATE learning_sessions SET status='COMPLETED',ended_at=CURRENT_TIMESTAMP WHERE id=$1`, fixture.security.sessionID)
	payload, _ := fixture.parentLive(t)
	assertLiveString(t, payload, "status", "COMPLETED")
	assertLiveString(t, payload, "detail_mode", "REPORT")
	assertLiveString(t, payload, "student_answer_visibility", "WITHHELD_NOT_ACTIVE")
	if _, present := payload["student_answer_preview"]; present {
		t.Fatal("student_answer_preview present after completion")
	}
	if payload["question_prompt"] != private.prompt || payload["correct_answer"] == nil || payload["full_solution"] != private.solution {
		t.Fatal("completed report lost question_prompt, correct_answer or full_solution")
	}
}
