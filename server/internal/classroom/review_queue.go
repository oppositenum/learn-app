package classroom

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oppositenum/ai-learning-tutor/server/internal/mastery"
)

func recordReviewFailure(ctx context.Context, tx pgx.Tx, row sessionRow, sessionID uuid.UUID, now time.Time) error {
	if row.evidenceForm != mastery.FormReview || row.reviewAttemptFailedAt != nil {
		return nil
	}
	skill, err := loadSkill(ctx, tx, row.studentID, row.knowledgePointID)
	if err != nil {
		return err
	}
	skill = mastery.NewEngine().Apply(skill, mastery.Evidence{Correct: false, Form: mastery.FormReview, At: now})
	if err := saveSkill(ctx, tx, row.studentID, row.knowledgePointID, skill, mastery.NewEngine().Score(skill), now); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE learning_sessions SET review_attempt_failed_at=$2 WHERE id=$1 AND review_attempt_failed_at IS NULL`, sessionID, now)
	return err
}

func resolveSuccessfulReview(ctx context.Context, tx pgx.Tx, row sessionRow, skill mastery.Skill, independent bool, now time.Time) error {
	if row.evidenceForm != mastery.FormReview {
		return nil
	}
	if skill.NextReviewAt == nil {
		return errors.New("review success did not produce a next review time")
	}
	result := "ASSISTED_SUCCESS"
	if independent {
		result = "INDEPENDENT_SUCCESS"
	}
	source, priority, err := closeReviewQueueItem(ctx, tx, row, result, now)
	if err != nil {
		return err
	}
	return scheduleReview(ctx, tx, row.studentID, row.knowledgePointID, source, *skill.NextReviewAt, priority)
}

func resolveFailedReviewOnAbandon(ctx context.Context, tx pgx.Tx, row lifecycleRow, now time.Time) (bool, error) {
	if row.reviewQueueID == nil || row.reviewAttemptFailedAt == nil {
		return false, nil
	}
	var knowledgePointID uuid.UUID
	var source string
	var priority int
	err := tx.QueryRow(ctx, `
UPDATE review_queue
SET status='COMPLETED',result='FAILED',resolved_at=$2,attempts=attempts+1
WHERE id=$1 AND student_id=$3 AND status='PENDING'
RETURNING knowledge_point_id,source,priority`, *row.reviewQueueID, now, row.studentID).Scan(&knowledgePointID, &source, &priority)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	skill, err := loadSkill(ctx, tx, row.studentID, knowledgePointID)
	if err != nil {
		return false, err
	}
	if skill.State != mastery.Regressed || skill.NextReviewAt == nil {
		skill = mastery.NewEngine().Apply(skill, mastery.Evidence{Correct: false, Form: mastery.FormReview, At: now})
		if err := saveSkill(ctx, tx, row.studentID, knowledgePointID, skill, mastery.NewEngine().Score(skill), now); err != nil {
			return false, err
		}
	}
	return true, scheduleReview(ctx, tx, row.studentID, knowledgePointID, source, *skill.NextReviewAt, priority)
}

func closeReviewQueueItem(ctx context.Context, tx pgx.Tx, row sessionRow, result string, now time.Time) (string, int, error) {
	if row.reviewQueueID == nil {
		return "", 0, errors.New("review session has no bound queue item")
	}
	var source string
	var priority int
	err := tx.QueryRow(ctx, `
UPDATE review_queue
SET status='COMPLETED',result=$2,resolved_at=$3,attempts=attempts+1
WHERE id=$1 AND student_id=$4 AND knowledge_point_id=$5 AND status='PENDING'
RETURNING source,priority`, *row.reviewQueueID, result, now, row.studentID, row.knowledgePointID).Scan(&source, &priority)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, fmt.Errorf("bound review queue item %s is not pending", *row.reviewQueueID)
	}
	return source, priority, err
}

func scheduleReview(ctx context.Context, tx pgx.Tx, studentID, knowledgePointID uuid.UUID, source string, dueAt time.Time, priority int) error {
	_, err := tx.Exec(ctx, `
INSERT INTO review_queue(id,student_id,knowledge_point_id,source,due_at,priority)
VALUES($1,$2,$3,$4,$5,$6)
ON CONFLICT(student_id,knowledge_point_id,source) WHERE status='PENDING'
DO UPDATE SET due_at=EXCLUDED.due_at,priority=EXCLUDED.priority`, uuid.New(), studentID, knowledgePointID, source, dueAt, priority)
	return err
}

func saveSkill(ctx context.Context, tx pgx.Tx, studentID, knowledgePointID uuid.UUID, skill mastery.Skill, score int, now time.Time) error {
	_, err := tx.Exec(ctx, `
INSERT INTO student_skill_states
    (student_id,knowledge_point_id,state,score_internal,independent_successes,assisted_successes,
     life_context_successes,variant_successes,textbook_successes,review_successes,
     consecutive_review_failures,next_review_at,last_seen_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
ON CONFLICT (student_id,knowledge_point_id) DO UPDATE SET
    state=EXCLUDED.state,score_internal=EXCLUDED.score_internal,
    independent_successes=EXCLUDED.independent_successes,assisted_successes=EXCLUDED.assisted_successes,
    life_context_successes=EXCLUDED.life_context_successes,variant_successes=EXCLUDED.variant_successes,
    textbook_successes=EXCLUDED.textbook_successes,review_successes=EXCLUDED.review_successes,
    consecutive_review_failures=EXCLUDED.consecutive_review_failures,
    next_review_at=EXCLUDED.next_review_at,last_seen_at=EXCLUDED.last_seen_at,
    version=student_skill_states.version+1`, studentID, knowledgePointID, skill.State, score,
		skill.IndependentSuccesses, skill.AssistedSuccesses, skill.LifeContextSuccesses,
		skill.VariantSuccesses, skill.TextbookSuccesses, skill.ReviewSuccesses,
		skill.ConsecutiveReviewFailures, skill.NextReviewAt, now)
	return err
}
