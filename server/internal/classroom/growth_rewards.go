package classroom

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oppositenum/ai-learning-tutor/server/internal/reward"
)

// earnedReward is one growth reward a completion earned. The source makes the
// same behaviour count once: reward_events keeps one row per type and source.
type earnedReward struct {
	rewardType reward.Type
	sourceID   string
}

// answerReward names how the child reached a correct answer. A hint comes
// first, so a correct answer after a hint is HINT_SUCCESS and never also a self
// correction. Only a first-try independent answer on an L0 or L1 question
// earns EFFORT; a harder one earns nothing here.
func answerReward(hinted, priorWrong, independent bool, difficulty, sourceID string) (earnedReward, bool) {
	switch {
	case hinted:
		return earnedReward{reward.HintSuccess, sourceID}, true
	case priorWrong:
		return earnedReward{reward.SelfCorrection, sourceID}, true
	case independent && (difficulty == "L0" || difficulty == "L1"):
		return earnedReward{reward.Effort, sourceID}, true
	}
	return earnedReward{}, false
}

// sessionWasHinted reports whether the classroom gave a hint or stronger help.
// A Socratic follow-up after a wrong answer raises assistance_level too, but it
// is not a hint, so a correct answer after it stays a self correction.
func sessionWasHinted(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID) (bool, error) {
	var hinted bool
	err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM tutor_turns
    WHERE session_id=$1 AND actor='TUTOR'
      AND action IN ('HINT','SCAFFOLD','ANALOGY','BACKTRACK','EXPLAIN','VOICE_EXPLAIN')
)`, sessionID).Scan(&hinted)
	return hinted, err
}

func questionDifficulty(ctx context.Context, tx pgx.Tx, questionID uuid.UUID) (string, error) {
	var difficulty string
	err := tx.QueryRow(ctx, `SELECT difficulty FROM questions WHERE id=$1`, questionID).Scan(&difficulty)
	return difficulty, err
}

// dailyCompletionReward is earned when the completed block was the last open
// block of its plan. Its source is the plan date, so it counts once a day even
// if the day's plan was replaced.
func dailyCompletionReward(ctx context.Context, tx pgx.Tx, planBlockID *uuid.UUID) (earnedReward, bool, error) {
	if planBlockID == nil {
		return earnedReward{}, false, nil
	}
	var planDate time.Time
	var allCompleted bool
	err := tx.QueryRow(ctx, `
SELECT plan.plan_date,bool_and(block.status='COMPLETED')
FROM learning_plan_blocks completed
JOIN learning_plans plan ON plan.id=completed.plan_id
JOIN learning_plan_blocks block ON block.plan_id=plan.id
WHERE completed.id=$1
GROUP BY plan.plan_date`, *planBlockID).Scan(&planDate, &allCompleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return earnedReward{}, false, nil
	}
	if err != nil || !allCompleted {
		return earnedReward{}, false, err
	}
	return earnedReward{reward.DailyCompletion, "DAILY:" + planDate.Format("2006-01-02")}, true, nil
}

// grantRewards records each earned reward once and returns the compatibility
// energy total afterwards.
func grantRewards(ctx context.Context, tx pgx.Tx, studentID, sessionID uuid.UUID, rewards []earnedReward) (int, error) {
	for _, earned := range rewards {
		if _, err := grantReward(ctx, tx, studentID, sessionID, earned.rewardType, earned.sourceID); err != nil {
			return 0, err
		}
	}
	var energy int
	err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT total_energy FROM student_growth WHERE student_id=$1),0)`, studentID).Scan(&energy)
	return energy, err
}

// headlineReward keeps the single reward_type the realtime event has always
// carried: a cross-subject return, then mastery, then the answer reward.
func headlineReward(rewards []earnedReward) (reward.Type, string) {
	for _, preferred := range []reward.Type{reward.CrossSubjectInsight, reward.Mastery} {
		for _, earned := range rewards {
			if earned.rewardType == preferred {
				return earned.rewardType, earned.sourceID
			}
		}
	}
	if len(rewards) == 0 {
		return "", ""
	}
	return rewards[0].rewardType, rewards[0].sourceID
}

func rewardTypes(rewards []earnedReward) []reward.Type {
	types := make([]reward.Type, 0, len(rewards))
	for _, earned := range rewards {
		types = append(types, earned.rewardType)
	}
	return types
}
