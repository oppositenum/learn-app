package integration

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oppositenum/ai-learning-tutor/server/internal/classroom"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

func TestB2MisconceptionCodesAreValidatedAndRejectedCodesAreObservable(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	var studentUserID, knowledgePointID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT student.user_id,question.knowledge_point_id FROM learning_sessions session JOIN students student ON student.id=session.student_id JOIN questions question ON question.id=session.current_question_id WHERE session.id=$1`, fixture.sessionID).Scan(&studentUserID, &knowledgePointID); err != nil {
		t.Fatal(err)
	}
	secondValidID := uuid.New()
	reported, err := json.Marshal([]string{"FIXED_COST_IGNORED", "SECOND_VALID_CODE", fixture.privateCanary, "ADD_DENOMINATORS_DIRECTLY", "FIXED_COST_IGNORED"})
	if err != nil {
		t.Fatal(err)
	}
	if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO misconceptions(id,code,name,description) VALUES($1,'SECOND_VALID_CODE','第二个合法误概念','集成测试')`, secondValidID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO knowledge_misconception_links(knowledge_point_id,misconception_id) VALUES($1,$2)`, knowledgePointID, secondValidID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE question_private_answers SET misconceptions_private_json=$2::jsonb WHERE question_id=$1`, fixture.releasedQuestionID, string(reported))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := classroom.NewService(pool, nil, nil, nil).Submit(ctx, studentUserID, fixture.sessionID, "wrong"); err != nil {
		t.Fatal(err)
	}
	var validRows, invalidRows, pendingReviews int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM student_misconceptions state JOIN misconceptions misconception ON misconception.id=state.misconception_id WHERE state.student_id=$1 AND state.knowledge_point_id=$2 AND misconception.code IN ('FIXED_COST_IGNORED','SECOND_VALID_CODE')`, fixture.studentID, knowledgePointID).Scan(&validRows); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM student_misconceptions state JOIN misconceptions misconception ON misconception.id=state.misconception_id WHERE state.student_id=$1 AND state.knowledge_point_id=$2 AND misconception.code='ADD_DENOMINATORS_DIRECTLY'`, fixture.studentID, knowledgePointID).Scan(&invalidRows); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM review_queue WHERE student_id=$1 AND knowledge_point_id=$2 AND source='MISCONCEPTION' AND status='PENDING'`, fixture.studentID, knowledgePointID).Scan(&pendingReviews); err != nil {
		t.Fatal(err)
	}
	if validRows != 2 || invalidRows != 0 || pendingReviews != 1 {
		t.Fatalf("taxonomy valid=%d invalid=%d pending reviews=%d", validRows, invalidRows, pendingReviews)
	}
	var unknownEvents, crossEvents, rawCodes, answerColumns int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE reason='UNKNOWN_CODE'),count(*) FILTER(WHERE reason='CROSS_KNOWLEDGE_POINT'),count(*) FILTER(WHERE code_hash IN ($2,'ADD_DENOMINATORS_DIRECTLY')) FROM misconception_quality_events WHERE session_id=$1`, fixture.sessionID, fixture.privateCanary).Scan(&unknownEvents, &crossEvents, &rawCodes); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='misconception_quality_events' AND column_name ILIKE '%answer%'`).Scan(&answerColumns); err != nil {
		t.Fatal(err)
	}
	var leakedBody bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM misconception_quality_events WHERE session_id=$1 AND row_to_json(misconception_quality_events)::text LIKE '%' || $2 || '%')`, fixture.sessionID, fixture.privateCanary).Scan(&leakedBody); err != nil {
		t.Fatal(err)
	}
	if unknownEvents != 1 || crossEvents != 1 || rawCodes != 0 || answerColumns != 0 || leakedBody {
		t.Fatalf("quality unknown=%d cross=%d raw=%d answer_columns=%d leaked=%v", unknownEvents, crossEvents, rawCodes, answerColumns, leakedBody)
	}
	var crossRecognized, unknownRecognized int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE reason='CROSS_KNOWLEDGE_POINT' AND recognized_misconception_id IS NOT NULL),count(*) FILTER(WHERE reason='UNKNOWN_CODE' AND recognized_misconception_id IS NOT NULL) FROM misconception_quality_events WHERE session_id=$1`, fixture.sessionID).Scan(&crossRecognized, &unknownRecognized); err != nil {
		t.Fatal(err)
	}
	if crossRecognized != 1 || unknownRecognized != 0 {
		t.Fatalf("quality recognized cross=%d unknown=%d", crossRecognized, unknownRecognized)
	}
}

func TestB2RejectedMisconceptionCodesDoNotCreateReviewState(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	var studentUserID, knowledgePointID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT student.user_id,question.knowledge_point_id FROM learning_sessions session JOIN students student ON student.id=session.student_id JOIN questions question ON question.id=session.current_question_id WHERE session.id=$1`, fixture.sessionID).Scan(&studentUserID, &knowledgePointID); err != nil {
		t.Fatal(err)
	}
	reported, err := json.Marshal([]string{fixture.privateCanary, "ADD_DENOMINATORS_DIRECTLY"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE question_private_answers SET misconceptions_private_json=$2::jsonb WHERE question_id=$1`, fixture.releasedQuestionID, string(reported)); err != nil {
		t.Fatal(err)
	}
	if _, err := classroom.NewService(pool, nil, nil, nil).Submit(ctx, studentUserID, fixture.sessionID, "wrong"); err != nil {
		t.Fatal(err)
	}
	var taxonomyRows, reviewRows, qualityRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM student_misconceptions WHERE student_id=$1 AND knowledge_point_id=$2`, fixture.studentID, knowledgePointID).Scan(&taxonomyRows); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM review_queue WHERE student_id=$1 AND knowledge_point_id=$2 AND source='MISCONCEPTION'`, fixture.studentID, knowledgePointID).Scan(&reviewRows); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM misconception_quality_events WHERE session_id=$1`, fixture.sessionID).Scan(&qualityRows); err != nil {
		t.Fatal(err)
	}
	if taxonomyRows != 0 || reviewRows != 0 || qualityRows != 2 {
		t.Fatalf("rejected codes taxonomy=%d reviews=%d quality=%d", taxonomyRows, reviewRows, qualityRows)
	}
}
