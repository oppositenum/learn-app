package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
	"github.com/oppositenum/ai-learning-tutor/server/internal/api"
	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
	"github.com/oppositenum/ai-learning-tutor/server/internal/classroom"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/internal/parent"
	"github.com/oppositenum/ai-learning-tutor/server/internal/planner"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

const (
	firstTryFeedback  = "你自己把这道题做对了。"
	correctedFeedback = "你把刚才没做对的地方改对了。"
	explainedFeedback = "你把做法说清楚了，结果也对。"
)

// An answer the key does not match, so only the model analysis can accept it.
const reasonedAnswer = "我先看每秒走多少，再乘上时间来算。"

type textFeedbackClassroom struct {
	pool      *pgxpool.Pool
	router    http.Handler
	agent     *provenanceTeachingAgent
	token     string
	sessionID uuid.UUID
	question  uuid.UUID
}

// startTextFeedbackClassroom opens a plain text classroom on one released
// question, answered through a fake teaching agent.
func startTextFeedbackClassroom(t *testing.T) textFeedbackClassroom {
	t.Helper()
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET status='ABANDONED',ended_at=now() WHERE id=$1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	question := uuid.MustParse("40000000-0000-4000-8000-000000000010")
	sessionID := uuid.New()
	if _, err := pool.Exec(ctx, `
INSERT INTO learning_sessions(id,student_id,subject_id,current_question_id,status,target_minutes,current_state,active_task_id,evidence_form)
SELECT $1,$2,kp.subject_id,q.id,'ACTIVE',20,'ASK',kp.id,'LIFE' FROM questions q JOIN knowledge_points kp ON kp.id=q.knowledge_point_id WHERE q.id=$3`, sessionID, fixture.studentID, question); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO tutor_turns(id,session_id,sequence,actor,action,message,reason_private) SELECT $1,$2,1,'TUTOR','ASK',prompt_public,'integration question' FROM questions WHERE id=$3`, uuid.New(), sessionID, question); err != nil {
		t.Fatal(err)
	}
	agent := &provenanceTeachingAgent{}
	plannerService := planner.NewService(pool)
	service := classroom.NewService(pool, nil, nil, nil, plannerService).WithTeachingAgent(agent)
	router := api.NewRouter(api.Dependencies{Authenticate: auth.NewSessionAuthenticator(pool).Middleware, Classroom: classroom.NewHandler(service, pool, parent.NewRepository(pool), plannerService)})
	return textFeedbackClassroom{pool: pool, router: router, agent: agent, token: fixture.studentToken, sessionID: sessionID, question: question}
}

func (classroomUnderTest textFeedbackClassroom) submit(t *testing.T, answer string) classroom.SubmitResult {
	t.Helper()
	response := performJSON(classroomUnderTest.router, http.MethodPost, "/api/v1/student/sessions/"+classroomUnderTest.sessionID.String()+"/answers", classroomUnderTest.token, map[string]any{"answer": answer})
	if response.Code != http.StatusOK {
		t.Fatalf("submit=%d %s", response.Code, response.Body.String())
	}
	assertStudentPayloadHasNoPrivateFields(t, response.Body.Bytes())
	var result classroom.SubmitResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

// assertCorrectFeedback checks the completing turn says want, carries no
// answer or question text, and is what the classroom page reads back.
func (classroomUnderTest textFeedbackClassroom) assertCorrectFeedback(t *testing.T, result classroom.SubmitResult, want string) {
	t.Helper()
	if result.Action != "COMPLETE" || result.Message != want {
		t.Fatalf("correct feedback action=%s message=%q want %q", result.Action, result.Message, want)
	}
	var referenceAnswer, fullSolution, prompt string
	if err := classroomUnderTest.pool.QueryRow(context.Background(), `SELECT a.teacher_reference_answer,a.full_solution_private,q.prompt_public FROM question_private_answers a JOIN questions q ON q.id=a.question_id WHERE q.id=$1`, classroomUnderTest.question).Scan(&referenceAnswer, &fullSolution, &prompt); err != nil {
		t.Fatal(err)
	}
	for index, text := range []string{referenceAnswer, fullSolution, prompt} {
		if text == "" || strings.Contains(result.Message, text) {
			t.Fatalf("correct feedback carries guarded text #%d", index)
		}
	}
	read := performJSON(classroomUnderTest.router, http.MethodGet, "/api/v1/student/sessions/"+classroomUnderTest.sessionID.String(), classroomUnderTest.token, nil)
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), want) {
		t.Fatalf("classroom read=%d does not show the correct feedback", read.Code)
	}
	assertStudentPayloadHasNoPrivateFields(t, read.Body.Bytes())
}

func TestFirstTryCorrectAnswerSaysTheChildDidItThemselves(t *testing.T) {
	classroomUnderTest := startTextFeedbackClassroom(t)
	answer := privateTeacherAnswer(t, context.Background(), classroomUnderTest.pool, classroomUnderTest.question)
	result := classroomUnderTest.submit(t, answer)
	// A key match stores STRONG without any model reading the reasoning, so
	// it must not claim the reasoning was explained.
	classroomUnderTest.assertCorrectFeedback(t, result, firstTryFeedback)
	if strings.Contains(result.Message, "说清楚了") || classroomUnderTest.agent.analyzeCalls != 0 || classroomUnderTest.agent.generateCalls != 0 {
		t.Fatalf("first try feedback=%q analyze=%d generate=%d", result.Message, classroomUnderTest.agent.analyzeCalls, classroomUnderTest.agent.generateCalls)
	}
}

func TestCorrectAnswerAfterAWrongAnswerSaysTheChildFixedIt(t *testing.T) {
	classroomUnderTest := startTextFeedbackClassroom(t)
	classroomUnderTest.agent.analysis = ai.AnalyzeAnswerResult{AnswerCorrect: false, ReasoningQuality: "WEAK", Confidence: .95, ErrorType: "CALCULATION", Misconceptions: []string{"REASONING_GAP"}, EmotionSignal: "NEUTRAL", Engagement: "NORMAL", WeaknessLayer: "L2"}
	if wrong := classroomUnderTest.submit(t, "30/2"); wrong.Action != "PROBE" {
		t.Fatalf("wrong answer action=%s", wrong.Action)
	}
	generated := classroomUnderTest.agent.generateCalls
	answer := privateTeacherAnswer(t, context.Background(), classroomUnderTest.pool, classroomUnderTest.question)
	result := classroomUnderTest.submit(t, answer)
	classroomUnderTest.assertCorrectFeedback(t, result, correctedFeedback)
	if classroomUnderTest.agent.generateCalls != generated {
		t.Fatal("the correct answer generated a model turn")
	}
}

func TestCorrectAnswerAfterAnEmotionBreakStillSaysTheChildFixedIt(t *testing.T) {
	classroomUnderTest := startTextFeedbackClassroom(t)
	classroomUnderTest.agent.analysis = ai.AnalyzeAnswerResult{AnswerCorrect: false, ReasoningQuality: "WEAK", Confidence: .95, ErrorType: "CALCULATION", Misconceptions: []string{"REASONING_GAP"}, EmotionSignal: "FRUSTRATED", Engagement: "NORMAL", WeaknessLayer: "L2"}
	if wrong := classroomUnderTest.submit(t, "30/2"); wrong.Action != "BREAK" {
		t.Fatalf("frustrated wrong answer action=%s", wrong.Action)
	}
	var assistance int
	if err := classroomUnderTest.pool.QueryRow(context.Background(), `SELECT assistance_level FROM learning_sessions WHERE id=$1`, classroomUnderTest.sessionID).Scan(&assistance); err != nil {
		t.Fatal(err)
	}
	if assistance != 0 {
		t.Fatalf("a break raised assistance to %d; this case no longer covers the recorded wrong answer", assistance)
	}
	answer := privateTeacherAnswer(t, context.Background(), classroomUnderTest.pool, classroomUnderTest.question)
	classroomUnderTest.assertCorrectFeedback(t, classroomUnderTest.submit(t, answer), correctedFeedback)
}

func TestCorrectAnswerAfterAHintSaysTheChildFixedIt(t *testing.T) {
	classroomUnderTest := startTextFeedbackClassroom(t)
	if hint := classroomUnderTest.submit(t, "我不会"); hint.Action != "HINT" {
		t.Fatalf("help request action=%s", hint.Action)
	}
	answer := privateTeacherAnswer(t, context.Background(), classroomUnderTest.pool, classroomUnderTest.question)
	classroomUnderTest.assertCorrectFeedback(t, classroomUnderTest.submit(t, answer), correctedFeedback)
}

func TestCorrectAnswerWithStrongReasoningSaysTheChildExplainedIt(t *testing.T) {
	classroomUnderTest := startTextFeedbackClassroom(t)
	classroomUnderTest.agent.analysis = ai.AnalyzeAnswerResult{AnswerCorrect: true, ReasoningQuality: "STRONG", Confidence: .95, ErrorType: "NONE", EmotionSignal: "NEUTRAL", Engagement: "NORMAL", WeaknessLayer: ai.WeaknessLayerNone}
	result := classroomUnderTest.submit(t, reasonedAnswer)
	classroomUnderTest.assertCorrectFeedback(t, result, explainedFeedback)
	if classroomUnderTest.agent.analyzeCalls != 1 || classroomUnderTest.agent.generateCalls != 0 {
		t.Fatalf("explained feedback analyze=%d generate=%d; the sentence must come from the existing analysis only", classroomUnderTest.agent.analyzeCalls, classroomUnderTest.agent.generateCalls)
	}
}

func TestCorrectAnswerWithoutStrongReasoningDoesNotClaimItWasExplained(t *testing.T) {
	classroomUnderTest := startTextFeedbackClassroom(t)
	classroomUnderTest.agent.analysis = ai.AnalyzeAnswerResult{AnswerCorrect: true, ReasoningQuality: "PARTIAL", Confidence: .95, ErrorType: "NONE", EmotionSignal: "NEUTRAL", Engagement: "NORMAL", WeaknessLayer: ai.WeaknessLayerNone}
	result := classroomUnderTest.submit(t, reasonedAnswer)
	classroomUnderTest.assertCorrectFeedback(t, result, firstTryFeedback)
	if strings.Contains(result.Message, "说清楚了") {
		t.Fatal("PARTIAL reasoning was reported as explained")
	}
}
