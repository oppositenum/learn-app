package parent

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// OverviewDTO is what the parent home page reads: whether the child is in a
// classroom now and how today's plan is going. It carries no question text,
// answers or child words; those stay on the live classroom page.
type OverviewDTO struct {
	StudentID    uuid.UUID           `json:"student_id"`
	LearningDate string              `json:"learning_date"`
	Session      *OverviewSessionDTO `json:"session"`
	TodayPlan    *OverviewPlanDTO    `json:"today_plan"`
}

// OverviewSessionDTO is the open classroom. Status is ACTIVE while the child
// is working and PAUSED once the classroom is paused or has gone quiet long
// enough for stale-session recovery to pause it.
type OverviewSessionDTO struct {
	SessionID      uuid.UUID       `json:"session_id"`
	Status         string          `json:"status"`
	Subject        string          `json:"subject"`
	KnowledgePoint string          `json:"knowledge_point"`
	ActiveSeconds  int             `json:"active_seconds"`
	TargetMinutes  int16           `json:"target_minutes"`
	SocraticRound  int16           `json:"socratic_round"`
	TutorAction    string          `json:"tutor_action"`
	ErrorType      string          `json:"error_type"`
	Misconceptions json.RawMessage `json:"misconceptions"`
}

type OverviewPlanDTO struct {
	PlanID        uuid.UUID          `json:"plan_id"`
	Date          string             `json:"date"`
	TargetMinutes int                `json:"target_minutes"`
	Blocks        []OverviewBlockDTO `json:"blocks"`
}

// OverviewBlockDTO keeps the stored block status and adds the progress a
// parent reads. A paused classroom returns its block to AVAILABLE, so a block
// is PAUSED when its latest classroom is the paused open one.
type OverviewBlockDTO struct {
	ID             uuid.UUID  `json:"id"`
	Sequence       int        `json:"sequence"`
	Subject        string     `json:"subject"`
	KnowledgePoint string     `json:"knowledge_point"`
	Minutes        int        `json:"minutes"`
	Status         string     `json:"status"`
	Progress       string     `json:"progress"`
	SessionID      *uuid.UUID `json:"session_id"`
}

const (
	ProgressNotStarted = "NOT_STARTED"
	ProgressInProgress = "IN_PROGRESS"
	ProgressPaused     = "PAUSED"
	ProgressCompleted  = "COMPLETED"
)

func (repository *Repository) Overview(ctx context.Context, studentID uuid.UUID) (OverviewDTO, error) {
	overview := OverviewDTO{StudentID: studentID}
	if err := repository.pool.QueryRow(ctx, `SELECT (CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date::text`).Scan(&overview.LearningDate); err != nil {
		return overview, err
	}
	session, err := repository.openSession(ctx, studentID)
	if err != nil {
		return overview, err
	}
	overview.Session = session
	plan, err := repository.todayPlan(ctx, studentID, session)
	if err != nil {
		return overview, err
	}
	overview.TodayPlan = plan
	return overview, nil
}

// openSession reads the open classroom through the live classroom query, so
// the round, time and judgement are the ones the live page shows.
func (repository *Repository) openSession(ctx context.Context, studentID uuid.UUID) (*OverviewSessionDTO, error) {
	var sessionID uuid.UUID
	var status string
	var fresh bool
	err := repository.pool.QueryRow(ctx, `
SELECT id,status,last_activity_at>=CURRENT_TIMESTAMP-interval '90 seconds'
FROM learning_sessions
WHERE student_id=$1 AND status IN('ACTIVE','PAUSED')
ORDER BY started_at DESC LIMIT 1`, studentID).Scan(&sessionID, &status, &fresh)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	live, err := repository.LiveSession(ctx, studentID, sessionID)
	if err != nil {
		return nil, err
	}
	if status == "ACTIVE" && !fresh {
		status = "PAUSED"
	}
	action := live.TutorAction
	if action == "" {
		action = live.CurrentState
	}
	misconceptions := live.Misconceptions
	if len(misconceptions) == 0 {
		misconceptions = json.RawMessage(`[]`)
	}
	return &OverviewSessionDTO{
		SessionID:      live.SessionID,
		Status:         status,
		Subject:        live.Subject,
		KnowledgePoint: live.KnowledgePoint,
		ActiveSeconds:  live.ActiveSeconds,
		TargetMinutes:  live.TargetMinutes,
		SocraticRound:  live.SocraticRound,
		TutorAction:    action,
		ErrorType:      live.ErrorType,
		Misconceptions: misconceptions,
	}, nil
}

func (repository *Repository) todayPlan(ctx context.Context, studentID uuid.UUID, session *OverviewSessionDTO) (*OverviewPlanDTO, error) {
	var plan OverviewPlanDTO
	err := repository.pool.QueryRow(ctx, `
SELECT id,plan_date::text,target_minutes FROM learning_plans
WHERE student_id=$1 AND plan_date=(CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date AND status IN('PROPOSED','ACTIVE')
ORDER BY created_at DESC LIMIT 1`, studentID).Scan(&plan.PlanID, &plan.Date, &plan.TargetMinutes)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := repository.pool.Query(ctx, `
SELECT b.id,b.sequence,s.name_zh,COALESCE(kp.name,''),b.minutes,b.status,latest.id,latest.status
FROM learning_plan_blocks b
JOIN subjects s ON s.id=b.subject_id
LEFT JOIN knowledge_points kp ON kp.id=b.knowledge_point_id
LEFT JOIN LATERAL (
    SELECT id,status FROM learning_sessions WHERE plan_block_id=b.id ORDER BY started_at DESC LIMIT 1
) latest ON true
WHERE b.plan_id=$1 ORDER BY b.sequence`, plan.PlanID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	plan.Blocks = []OverviewBlockDTO{}
	for rows.Next() {
		var block OverviewBlockDTO
		var sessionStatus *string
		if err := rows.Scan(&block.ID, &block.Sequence, &block.Subject, &block.KnowledgePoint, &block.Minutes, &block.Status, &block.SessionID, &sessionStatus); err != nil {
			return nil, err
		}
		block.Progress = blockProgress(block, sessionStatus, session)
		plan.Blocks = append(plan.Blocks, block)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &plan, nil
}

// blockProgress names a block's progress from its stored status. The block of
// the open classroom follows that classroom, so the plan and the classroom
// card never disagree.
func blockProgress(block OverviewBlockDTO, sessionStatus *string, session *OverviewSessionDTO) string {
	if block.Status == "COMPLETED" {
		return ProgressCompleted
	}
	if session != nil && block.SessionID != nil && *block.SessionID == session.SessionID {
		if session.Status == "ACTIVE" {
			return ProgressInProgress
		}
		return ProgressPaused
	}
	if block.Status == "ACTIVE" {
		return ProgressInProgress
	}
	if sessionStatus != nil && *sessionStatus == "PAUSED" {
		return ProgressPaused
	}
	return ProgressNotStarted
}
