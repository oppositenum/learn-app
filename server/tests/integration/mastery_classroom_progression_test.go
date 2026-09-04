package integration

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/oppositenum/ai-learning-tutor/server/internal/classroom"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/internal/mastery"
	"github.com/oppositenum/ai-learning-tutor/server/internal/planner"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

func TestMasteryRequiresFourRealClassroomEvidenceForms(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	var studentUserID, subjectID, knowledgePointID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT st.user_id,ls.subject_id,q.knowledge_point_id FROM learning_sessions ls JOIN students st ON st.id=ls.student_id JOIN questions q ON q.id=ls.current_question_id WHERE ls.id=$1`, fixture.sessionID).Scan(&studentUserID, &subjectID, &knowledgePointID); err != nil {
		t.Fatal(err)
	}
	service := classroom.NewService(pool, nil, nil, nil, planner.NewService(pool))
	forms := []mastery.Form{mastery.FormLife, mastery.FormVariant, mastery.FormTextbook, mastery.FormReview}
	states := []mastery.State{mastery.Learning, mastery.Understood, mastery.Understood, mastery.Mastered}
	energy := []int{2, 4, 6, 18}
	for index, form := range forms {
		sessionID := fixture.sessionID
		if index == 0 {
			if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET current_state='ASK',socratic_fail_count=0,assistance_level=0,evidence_form=$2 WHERE id=$1`, sessionID, form); err != nil {
				t.Fatal(err)
			}
		} else {
			sessionID = uuid.New()
			if _, err := pool.Exec(ctx, `INSERT INTO learning_sessions(id,student_id,subject_id,current_question_id,status,target_minutes,current_state,evidence_form)VALUES($1,$2,$3,$4,'ACTIVE',10,'ASK',$5)`, sessionID, fixture.studentID, subjectID, fixture.releasedQuestionID, form); err != nil {
				t.Fatal(err)
			}
		}
		result, err := service.Submit(ctx, studentUserID, sessionID, fixture.privateCanary)
		if err != nil {
			t.Fatalf("form %s submit: %v", form, err)
		}
		if result.MasteryState != states[index] || result.Energy != energy[index] {
			t.Fatalf("form %s result state=%s energy=%d want=%s/%d", form, result.MasteryState, result.Energy, states[index], energy[index])
		}
		if index < len(forms)-1 && result.MasteryState == mastery.Mastered {
			t.Fatalf("form %s mastered before all evidence existed", form)
		}
	}
	var life, variant, textbook, review, score int
	if err := pool.QueryRow(ctx, `SELECT life_context_successes,variant_successes,textbook_successes,review_successes,score_internal::integer FROM student_skill_states WHERE student_id=$1 AND knowledge_point_id=$2`, fixture.studentID, knowledgePointID).Scan(&life, &variant, &textbook, &review, &score); err != nil {
		t.Fatal(err)
	}
	if life != 1 || variant != 1 || textbook != 1 || review != 1 || score != 100 {
		t.Fatalf("evidence counts life=%d variant=%d textbook=%d review=%d score=%d", life, variant, textbook, review, score)
	}
	var masteryRewards int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM reward_events WHERE student_id=$1 AND type='MASTERY'`, fixture.studentID).Scan(&masteryRewards); err != nil || masteryRewards != 1 {
		t.Fatalf("mastery rewards=%d err=%v", masteryRewards, err)
	}
}
