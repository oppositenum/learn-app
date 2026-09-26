package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
)

var wrongAnswerAnalysis = ai.AnalyzeAnswerResult{AnswerCorrect: false, ReasoningQuality: "WEAK", Confidence: .95, ErrorType: "CALCULATION", Misconceptions: []string{"REASONING_GAP"}, EmotionSignal: "NEUTRAL", Engagement: "NORMAL", WeaknessLayer: "L2"}

func (classroomUnderTest textFeedbackClassroom) setDifficulty(t *testing.T, difficulty string) {
	t.Helper()
	if _, err := classroomUnderTest.pool.Exec(context.Background(), `UPDATE questions SET difficulty=$2 WHERE id=$1`, classroomUnderTest.question, difficulty); err != nil {
		t.Fatal(err)
	}
}

func (classroomUnderTest textFeedbackClassroom) answerCorrectly(t *testing.T) {
	t.Helper()
	answer := privateTeacherAnswer(t, context.Background(), classroomUnderTest.pool, classroomUnderTest.question)
	if result := classroomUnderTest.submit(t, answer); result.Action != "COMPLETE" {
		t.Fatalf("correct answer action=%s", result.Action)
	}
}

// sessionRewards lists the rewards a session earned as TYPE=points, sorted.
func (classroomUnderTest textFeedbackClassroom) sessionRewards(t *testing.T, sessionID uuid.UUID) []string {
	t.Helper()
	return sessionRewardRows(t, classroomUnderTest.pool, sessionID)
}

func sessionRewardRows(t *testing.T, pool *pgxpool.Pool, sessionID uuid.UUID) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT type,points FROM reward_events WHERE session_id=$1`, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	rewards := []string{}
	for rows.Next() {
		var rewardType string
		var points int
		if err := rows.Scan(&rewardType, &points); err != nil {
			t.Fatal(err)
		}
		rewards = append(rewards, fmt.Sprintf("%s=%d", rewardType, points))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(rewards)
	return rewards
}

func assertRewards(t *testing.T, got []string, want ...string) {
	t.Helper()
	sort.Strings(want)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("rewards=%v want %v", got, want)
	}
}

// startSiblingSession opens another plain classroom on the same question for
// the same student, bound to planBlockID when it is not nil.
func (classroomUnderTest textFeedbackClassroom) startSiblingSession(t *testing.T, planBlockID *uuid.UUID) textFeedbackClassroom {
	t.Helper()
	ctx := context.Background()
	sessionID := uuid.New()
	if _, err := classroomUnderTest.pool.Exec(ctx, `
INSERT INTO learning_sessions(id,student_id,subject_id,current_question_id,status,target_minutes,current_state,active_task_id,evidence_form,plan_block_id)
SELECT $1,session.student_id,session.subject_id,session.current_question_id,'ACTIVE',20,'ASK',session.active_task_id,'LIFE',$3
FROM learning_sessions session WHERE session.id=$2`, sessionID, classroomUnderTest.sessionID, planBlockID); err != nil {
		t.Fatal(err)
	}
	if _, err := classroomUnderTest.pool.Exec(ctx, `INSERT INTO tutor_turns(id,session_id,sequence,actor,action,message,reason_private) SELECT $1,$2,1,'TUTOR','ASK',prompt_public,'integration question' FROM questions WHERE id=$3`, uuid.New(), sessionID, classroomUnderTest.question); err != nil {
		t.Fatal(err)
	}
	sibling := classroomUnderTest
	sibling.sessionID = sessionID
	return sibling
}

func TestFirstTryOnAnEasyQuestionEarnsEffort(t *testing.T) {
	for _, difficulty := range []string{"L0", "L1"} {
		t.Run(difficulty, func(t *testing.T) {
			classroomUnderTest := startTextFeedbackClassroom(t)
			classroomUnderTest.setDifficulty(t, difficulty)
			classroomUnderTest.answerCorrectly(t)
			assertRewards(t, classroomUnderTest.sessionRewards(t, classroomUnderTest.sessionID), "EFFORT=3")
		})
	}
}

func TestFirstTryOnAHardQuestionEarnsNoEffort(t *testing.T) {
	classroomUnderTest := startTextFeedbackClassroom(t)
	classroomUnderTest.setDifficulty(t, "L2")
	classroomUnderTest.answerCorrectly(t)
	assertRewards(t, classroomUnderTest.sessionRewards(t, classroomUnderTest.sessionID))
}

func TestCorrectAnswerAfterAWrongAnswerEarnsSelfCorrection(t *testing.T) {
	classroomUnderTest := startTextFeedbackClassroom(t)
	classroomUnderTest.setDifficulty(t, "L0")
	classroomUnderTest.agent.analysis = wrongAnswerAnalysis
	if wrong := classroomUnderTest.submit(t, "30/2"); wrong.Action != "PROBE" {
		t.Fatalf("wrong answer action=%s", wrong.Action)
	}
	classroomUnderTest.answerCorrectly(t)
	assertRewards(t, classroomUnderTest.sessionRewards(t, classroomUnderTest.sessionID), "SELF_CORRECTION=8")
}

func TestCorrectAnswerAfterABreakEarnsSelfCorrection(t *testing.T) {
	classroomUnderTest := startTextFeedbackClassroom(t)
	classroomUnderTest.setDifficulty(t, "L0")
	frustrated := wrongAnswerAnalysis
	frustrated.EmotionSignal = "FRUSTRATED"
	classroomUnderTest.agent.analysis = frustrated
	if wrong := classroomUnderTest.submit(t, "30/2"); wrong.Action != "BREAK" {
		t.Fatalf("frustrated wrong answer action=%s", wrong.Action)
	}
	classroomUnderTest.answerCorrectly(t)
	assertRewards(t, classroomUnderTest.sessionRewards(t, classroomUnderTest.sessionID), "SELF_CORRECTION=8")
}

func TestCorrectAnswerAfterAHintEarnsHintSuccessNotSelfCorrection(t *testing.T) {
	classroomUnderTest := startTextFeedbackClassroom(t)
	classroomUnderTest.setDifficulty(t, "L0")
	classroomUnderTest.agent.analysis = wrongAnswerAnalysis
	if wrong := classroomUnderTest.submit(t, "30/2"); wrong.Action != "PROBE" {
		t.Fatalf("wrong answer action=%s", wrong.Action)
	}
	if hint := classroomUnderTest.submit(t, "我不会"); hint.Action != "HINT" {
		t.Fatalf("help request action=%s", hint.Action)
	}
	classroomUnderTest.answerCorrectly(t)
	assertRewards(t, classroomUnderTest.sessionRewards(t, classroomUnderTest.sessionID), "HINT_SUCCESS=6")
}

func TestIndependentReviewEarnsDelayedReview(t *testing.T) {
	classroomUnderTest := startTextFeedbackClassroom(t)
	classroomUnderTest.setDifficulty(t, "L2")
	ctx := context.Background()
	queueID := uuid.New()
	if _, err := classroomUnderTest.pool.Exec(ctx, `
INSERT INTO review_queue(id,student_id,knowledge_point_id,source,due_at)
SELECT $1,session.student_id,question.knowledge_point_id,'MASTERY',now()-interval '1 second'
FROM learning_sessions session JOIN questions question ON question.id=session.current_question_id WHERE session.id=$2`, queueID, classroomUnderTest.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := classroomUnderTest.pool.Exec(ctx, `UPDATE learning_sessions SET evidence_form='REVIEW',review_queue_id=$2 WHERE id=$1`, classroomUnderTest.sessionID, queueID); err != nil {
		t.Fatal(err)
	}
	classroomUnderTest.answerCorrectly(t)
	assertRewards(t, classroomUnderTest.sessionRewards(t, classroomUnderTest.sessionID), "DELAYED_REVIEW=12")
	var result string
	if err := classroomUnderTest.pool.QueryRow(ctx, `SELECT result FROM review_queue WHERE id=$1`, queueID).Scan(&result); err != nil || result != "INDEPENDENT_SUCCESS" {
		t.Fatalf("review queue result=%s err=%v", result, err)
	}
}

func TestFinishingTheDaysLastBlockEarnsDailyCompletionOnce(t *testing.T) {
	classroomUnderTest := startTextFeedbackClassroom(t)
	classroomUnderTest.setDifficulty(t, "L2")
	ctx := context.Background()
	planID, firstBlock, lastBlock := uuid.New(), uuid.New(), uuid.New()
	if _, err := classroomUnderTest.pool.Exec(ctx, `
INSERT INTO learning_plans(id,student_id,plan_date,target_minutes,status)
SELECT $1,student_id,current_date,40,'ACTIVE' FROM learning_sessions WHERE id=$2`, planID, classroomUnderTest.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := classroomUnderTest.pool.Exec(ctx, `
INSERT INTO learning_plan_blocks(id,plan_id,sequence,subject_id,knowledge_point_id,minutes,mode,reason,status)
SELECT block.id,$1,block.sequence,session.subject_id,session.active_task_id,20,'CURRENT_GRADE','integration plan',block.status
FROM learning_sessions session,
     (VALUES($3::uuid,1::smallint,'ACTIVE'),($4::uuid,2::smallint,'AVAILABLE')) AS block(id,sequence,status)
WHERE session.id=$2`, planID, classroomUnderTest.sessionID, firstBlock, lastBlock); err != nil {
		t.Fatal(err)
	}
	if _, err := classroomUnderTest.pool.Exec(ctx, `UPDATE learning_sessions SET plan_block_id=$2 WHERE id=$1`, classroomUnderTest.sessionID, firstBlock); err != nil {
		t.Fatal(err)
	}
	classroomUnderTest.answerCorrectly(t)
	assertRewards(t, classroomUnderTest.sessionRewards(t, classroomUnderTest.sessionID))

	last := classroomUnderTest.startSiblingSession(t, &lastBlock)
	last.answerCorrectly(t)
	assertRewards(t, last.sessionRewards(t, last.sessionID), "DAILY_COMPLETION=15")

	// Finishing the day's plan again the same day adds nothing.
	if _, err := classroomUnderTest.pool.Exec(ctx, `UPDATE learning_plan_blocks SET status='ACTIVE' WHERE id=$1`, lastBlock); err != nil {
		t.Fatal(err)
	}
	again := classroomUnderTest.startSiblingSession(t, &lastBlock)
	again.answerCorrectly(t)
	assertRewards(t, again.sessionRewards(t, again.sessionID))
	var daily int
	if err := classroomUnderTest.pool.QueryRow(ctx, `SELECT count(*) FROM reward_events WHERE type='DAILY_COMPLETION' AND student_id=(SELECT student_id FROM learning_sessions WHERE id=$1)`, classroomUnderTest.sessionID).Scan(&daily); err != nil || daily != 1 {
		t.Fatalf("daily completion rewards=%d err=%v", daily, err)
	}
}

func TestNewPointsDoNotRewriteRecordedRewards(t *testing.T) {
	classroomUnderTest := startTextFeedbackClassroom(t)
	classroomUnderTest.setDifficulty(t, "L0")
	ctx := context.Background()
	oldSession := uuid.New()
	if _, err := classroomUnderTest.pool.Exec(ctx, `
INSERT INTO learning_sessions(id,student_id,subject_id,current_question_id,status,target_minutes,current_state,evidence_form,ended_at)
SELECT $1,student_id,subject_id,current_question_id,'COMPLETED',20,'COMPLETE','LIFE',now() FROM learning_sessions WHERE id=$2`, oldSession, classroomUnderTest.sessionID); err != nil {
		t.Fatal(err)
	}
	// A reward recorded under the retired table, and the energy it gave.
	if _, err := classroomUnderTest.pool.Exec(ctx, `
INSERT INTO reward_events(id,student_id,session_id,type,points,source_id)
SELECT $1,student_id,id,'EFFORT',2,id::text FROM learning_sessions WHERE id=$2`, uuid.New(), oldSession); err != nil {
		t.Fatal(err)
	}
	if _, err := classroomUnderTest.pool.Exec(ctx, `
INSERT INTO student_growth(student_id,total_energy,buildings_json)
SELECT student_id,2,'{}' FROM learning_sessions WHERE id=$1
ON CONFLICT(student_id) DO UPDATE SET total_energy=2`, oldSession); err != nil {
		t.Fatal(err)
	}
	classroomUnderTest.answerCorrectly(t)
	assertRewards(t, classroomUnderTest.sessionRewards(t, oldSession), "EFFORT=2")
	assertRewards(t, classroomUnderTest.sessionRewards(t, classroomUnderTest.sessionID), "EFFORT=3")
	var energy int
	if err := classroomUnderTest.pool.QueryRow(ctx, `SELECT total_energy FROM student_growth WHERE student_id=(SELECT student_id FROM learning_sessions WHERE id=$1)`, oldSession).Scan(&energy); err != nil || energy != 5 {
		t.Fatalf("compatibility energy=%d err=%v; want the old 2 kept and the new 3 added", energy, err)
	}
}

// The growth page reads counts and where each came from, never what the child
// wrote or the reward arithmetic behind the compatibility energy.
func TestGrowthPayloadCarriesNoAnswersScoresRanksOrBadges(t *testing.T) {
	classroomUnderTest := startTextFeedbackClassroom(t)
	classroomUnderTest.setDifficulty(t, "L0")
	classroomUnderTest.agent.analysis = wrongAnswerAnalysis
	const childWords = "我猜是三十除以二"
	if wrong := classroomUnderTest.submit(t, childWords); wrong.Action != "PROBE" {
		t.Fatalf("wrong answer action=%s", wrong.Action)
	}
	classroomUnderTest.answerCorrectly(t)

	response := performJSON(classroomUnderTest.router, http.MethodGet, "/api/v1/student/growth", classroomUnderTest.token, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("growth=%d %s", response.Code, response.Body.String())
	}
	assertStudentPayloadHasNoPrivateFields(t, response.Body.Bytes())
	body := response.Body.String()
	var referenceAnswer, prompt string
	if err := classroomUnderTest.pool.QueryRow(context.Background(), `SELECT a.teacher_reference_answer,q.prompt_public FROM question_private_answers a JOIN questions q ON q.id=a.question_id WHERE q.id=$1`, classroomUnderTest.question).Scan(&referenceAnswer, &prompt); err != nil {
		t.Fatal(err)
	}
	for index, text := range []string{childWords, referenceAnswer, prompt} {
		if text == "" || strings.Contains(body, text) {
			t.Fatalf("growth payload carries guarded text #%d", index)
		}
	}
	for _, word := range []string{"经验值", "徽章", "排名", "等级"} {
		if strings.Contains(body, word) {
			t.Fatalf("growth payload carries %s", word)
		}
	}
	var tree any
	if err := json.Unmarshal(response.Body.Bytes(), &tree); err != nil {
		t.Fatal(err)
	}
	for _, key := range jsonKeys(tree) {
		if key == "points" {
			t.Fatal("growth payload has reward points")
		}
		for _, forbidden := range []string{"answer", "score", "rank", "badge", "level", "experience", "solution"} {
			if strings.Contains(strings.ToLower(key), forbidden) {
				t.Fatalf("growth payload has key %q", key)
			}
		}
	}
	var payload struct {
		TotalEnergy    int `json:"total_energy"`
		GrowthEvidence struct {
			Indicators []struct {
				Code   string `json:"code"`
				Events []struct {
					Subject        string `json:"subject"`
					KnowledgePoint string `json:"knowledge_point"`
					OccurredAt     string `json:"occurred_at"`
				} `json:"events"`
			} `json:"indicators"`
		} `json:"growth_evidence"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.GrowthEvidence.Indicators) != 5 {
		t.Fatalf("indicators=%d", len(payload.GrowthEvidence.Indicators))
	}
	for _, indicator := range payload.GrowthEvidence.Indicators {
		for _, event := range indicator.Events {
			if event.Subject == "" || event.KnowledgePoint == "" || event.OccurredAt == "" {
				t.Fatalf("%s event lacks subject, knowledge point or time: %+v", indicator.Code, event)
			}
		}
	}
	var energy int
	if err := classroomUnderTest.pool.QueryRow(context.Background(), `SELECT total_energy FROM student_growth sg JOIN learning_sessions s ON s.student_id=sg.student_id WHERE s.id=$1`, classroomUnderTest.sessionID).Scan(&energy); err != nil {
		t.Fatal(err)
	}
	if payload.TotalEnergy != energy || energy < 8 {
		t.Fatalf("growth total_energy=%d stored=%d", payload.TotalEnergy, energy)
	}
}

func jsonKeys(value any) []string {
	var keys []string
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			keys = append(keys, key)
			keys = append(keys, jsonKeys(child)...)
		}
	case []any:
		for _, child := range typed {
			keys = append(keys, jsonKeys(child)...)
		}
	}
	return keys
}
