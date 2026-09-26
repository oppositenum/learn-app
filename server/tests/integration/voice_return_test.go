package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
	"github.com/oppositenum/ai-learning-tutor/server/internal/classroom"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/internal/usage"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

// countingTeachingAgent fails every call and counts it, so a test can prove a
// path never reaches the generation model.
type countingTeachingAgent struct{ calls int }

var errUnexpectedTeachingCall = errors.New("teaching agent must not be called")

func (agent *countingTeachingAgent) AnalyzeAnswer(context.Context, ai.AnalyzeAnswerRequest) (ai.AnalyzeAnswerResult, error) {
	agent.calls++
	return ai.AnalyzeAnswerResult{}, errUnexpectedTeachingCall
}
func (agent *countingTeachingAgent) GenerateTurn(context.Context, ai.GenerateTurnRequest) (ai.TutorTurn, error) {
	agent.calls++
	return ai.TutorTurn{}, errUnexpectedTeachingCall
}
func (agent *countingTeachingAgent) GenerateAnalogy(context.Context, ai.AnalogyRequest) (ai.TutorTurn, error) {
	agent.calls++
	return ai.TutorTurn{}, errUnexpectedTeachingCall
}
func (agent *countingTeachingAgent) GenerateParallelExample(context.Context, ai.ExampleRequest) (ai.TutorTurn, error) {
	agent.calls++
	return ai.TutorTurn{}, errUnexpectedTeachingCall
}
func (agent *countingTeachingAgent) GenerateExplanation(context.Context, ai.ExplainRequest) (ai.Explanation, error) {
	agent.calls++
	return ai.Explanation{}, errUnexpectedTeachingCall
}

type voiceReturnSnapshot struct {
	state           string
	questionID      uuid.UUID
	rounds          int
	assistanceLevel int
	evidenceForm    string
	version         int64
	answers         int
	stageEvidence   int
	attemptEvents   int
	tutorOutputs    int
	rewards         int
	skillSuccesses  int
	speechOutputs   int
	usageRecords    int
}

func takeVoiceReturnSnapshot(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fixture securityFixture) voiceReturnSnapshot {
	t.Helper()
	var snapshot voiceReturnSnapshot
	if err := pool.QueryRow(ctx, `SELECT current_state,current_question_id,socratic_fail_count,assistance_level,evidence_form,version FROM learning_sessions WHERE id=$1`, fixture.sessionID).Scan(&snapshot.state, &snapshot.questionID, &snapshot.rounds, &snapshot.assistanceLevel, &snapshot.evidenceForm, &snapshot.version); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM student_answers WHERE session_id=$1),
		(SELECT count(*) FROM classroom_stage_evidence WHERE session_id=$1),
		(SELECT count(*) FROM learning_effect_events WHERE session_id=$1 AND event_type='TASK_ATTEMPT'),
		(SELECT count(*) FROM learning_effect_events WHERE session_id=$1 AND event_type='TUTOR_OUTPUT' AND classroom_state='RETURN'),
		(SELECT count(*) FROM reward_events WHERE student_id=$2),
		(SELECT COALESCE(sum(independent_successes+life_context_successes+variant_successes+textbook_successes),0) FROM student_skill_states WHERE student_id=$2),
		(SELECT count(*) FROM speech_outputs WHERE session_id=$1),
		(SELECT count(*) FROM ai_usage_records)`, fixture.sessionID, fixture.studentID).Scan(&snapshot.answers, &snapshot.stageEvidence, &snapshot.attemptEvents, &snapshot.tutorOutputs, &snapshot.rewards, &snapshot.skillSuccesses, &snapshot.speechOutputs, &snapshot.usageRecords); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

// 「我懂了」 only leaves a voice explanation: it returns to the same question
// without counting a Socratic round, writing success evidence, or calling the
// generation model or TTS. Outside VOICE_EXPLAIN it is refused and changes
// nothing.
func TestVoiceReturnKeepsOriginalQuestionWithoutEvidenceOrProviderCalls(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET current_state='VOICE_EXPLAIN',socratic_fail_count=3,assistance_level=2,version=7 WHERE id=$1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	var studentUserID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT user_id FROM students WHERE id=$1`, fixture.studentID).Scan(&studentUserID); err != nil {
		t.Fatal(err)
	}
	agent := &countingTeachingAgent{}
	voice := &trackingVoice{}
	service := classroom.NewService(pool, nil, voice, usage.NewRecorder(pool)).WithTeachingAgent(agent)

	before := takeVoiceReturnSnapshot(t, ctx, pool, fixture)
	if before.questionID != fixture.releasedQuestionID {
		t.Fatalf("fixture question=%s want %s", before.questionID, fixture.releasedQuestionID)
	}
	result, err := service.ReturnFromVoice(ctx, studentUserID, fixture.sessionID)
	if err != nil {
		t.Fatalf("voice return: %v", err)
	}
	if result.Action != "RETURN" || result.SocraticRound != 3 || result.Version != before.version+1 {
		t.Fatalf("voice return result action=%s round=%d version=%d", result.Action, result.SocraticRound, result.Version)
	}
	after := takeVoiceReturnSnapshot(t, ctx, pool, fixture)
	want := before
	want.state = "RETURN"
	want.version = before.version + 1
	// The only learning record is the Tutor's own RETURN sentence.
	want.tutorOutputs = before.tutorOutputs + 1
	if after != want {
		t.Fatalf("voice return changed more than state and version:\nbefore=%+v\nafter=%+v", before, after)
	}
	if agent.calls != 0 || voice.calls != 0 {
		t.Fatalf("voice return called teaching agent %d times and TTS %d times", agent.calls, voice.calls)
	}

	if _, err := service.ReturnFromVoice(ctx, studentUserID, fixture.sessionID); !errors.Is(err, classroom.ErrVoiceNotActive) {
		t.Fatalf("second voice return err=%v want ErrVoiceNotActive", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET current_state='ANALOGY' WHERE id=$1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	refusedBefore := takeVoiceReturnSnapshot(t, ctx, pool, fixture)
	if _, err := service.ReturnFromVoice(ctx, studentUserID, fixture.sessionID); !errors.Is(err, classroom.ErrVoiceNotActive) {
		t.Fatalf("voice return outside VOICE_EXPLAIN err=%v want ErrVoiceNotActive", err)
	}
	if refused := takeVoiceReturnSnapshot(t, ctx, pool, fixture); refused != refusedBefore || refused.questionID != fixture.releasedQuestionID {
		t.Fatalf("refused voice return changed the classroom:\nbefore=%+v\nafter=%+v", refusedBefore, refused)
	}
	if agent.calls != 0 || voice.calls != 0 {
		t.Fatalf("refused voice return called teaching agent %d times and TTS %d times", agent.calls, voice.calls)
	}
}
