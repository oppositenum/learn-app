package planner

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PersistedPlan struct {
	ID            uuid.UUID
	StudentID     uuid.UUID
	Date          time.Time
	TargetMinutes int
	Blocks        []PersistedBlock
}

type PersistedBlock struct {
	ID               uuid.UUID
	Sequence         int
	SubjectID        uuid.UUID
	SubjectCode      string
	KnowledgePointID *uuid.UUID
	Focus            string
	Minutes          int
	Mode             Mode
	Reason           string
	OriginalTaskID   *uuid.UUID
	Status           string
	SessionID        *uuid.UUID
	SessionStatus    *string
}

type PlanUpdate struct {
	PlanUpdated    bool
	TodayPreserved bool
	AppliesFrom    time.Time
}

type Service struct {
	pool   *pgxpool.Pool
	engine Engine
	now    func() time.Time
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, engine: Engine{}, now: time.Now}
}

func (service *Service) EnsureForUser(ctx context.Context, userID uuid.UUID, date time.Time) (PersistedPlan, error) {
	var studentID uuid.UUID
	if err := service.pool.QueryRow(ctx, `SELECT id FROM students WHERE user_id=$1`, userID).Scan(&studentID); err != nil {
		return PersistedPlan{}, err
	}
	return service.Ensure(ctx, studentID, date, uuid.Nil)
}

func (service *Service) Ensure(ctx context.Context, studentID uuid.UUID, date time.Time, basedOnSessionID uuid.UUID) (PersistedPlan, error) {
	plan, _, err := service.EnsureWithStatus(ctx, studentID, date, basedOnSessionID)
	return plan, err
}

func (service *Service) EnsureWithStatus(ctx context.Context, studentID uuid.UUID, date time.Time, basedOnSessionID uuid.UUID) (PersistedPlan, bool, error) {
	date = day(date)
	var persisted PersistedPlan
	created := false
	err := pgx.BeginFunc(ctx, service.pool, func(tx pgx.Tx) error {
		var lockedStudentID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM students WHERE id=$1 FOR NO KEY UPDATE`, studentID).Scan(&lockedStudentID); err != nil {
			return err
		}
		var err error
		persisted, created, err = service.ensureWithLocked(ctx, tx, studentID, date, basedOnSessionID)
		return err
	})
	return persisted, created && err == nil, err
}

func (service *Service) ensureWithLocked(ctx context.Context, tx pgx.Tx, studentID uuid.UUID, date time.Time, basedOnSessionID uuid.UUID) (PersistedPlan, bool, error) {
	if existing, err := loadWith(ctx, tx, studentID, date); err == nil {
		return existing, false, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return PersistedPlan{}, false, err
	}
	preferences, prioritySubjects, enabledSubjects, err := service.preferences(ctx, tx, studentID)
	if err != nil {
		return PersistedPlan{}, false, err
	}
	candidates, metadata, err := service.candidates(ctx, tx, studentID, date, prioritySubjects, enabledSubjects)
	if err != nil {
		return PersistedPlan{}, false, err
	}
	if len(candidates) == 0 && len(enabledSubjects) > 0 {
		// Enabled subjects may have no released content yet; never let a
		// parent setting leave the student without a plan.
		candidates, metadata, err = service.candidates(ctx, tx, studentID, date, prioritySubjects, nil)
		if err != nil {
			return PersistedPlan{}, false, err
		}
	}
	plan := service.engine.Build(Input{Date: date, Candidates: candidates, Preferences: preferences})
	if len(plan.Blocks) == 0 {
		return PersistedPlan{}, false, errors.New("no released curriculum candidates available")
	}
	persisted := PersistedPlan{ID: uuid.New(), StudentID: studentID, Date: plan.Date, TargetMinutes: plan.TargetMinutes}
	var basedOn any
	if basedOnSessionID != uuid.Nil {
		basedOn = basedOnSessionID
	}
	if _, err := tx.Exec(ctx, `INSERT INTO learning_plans(id,student_id,plan_date,target_minutes,status,based_on_session_id) VALUES($1,$2,$3,$4,'PROPOSED',$5)`, persisted.ID, studentID, date, plan.TargetMinutes, basedOn); err != nil {
		return PersistedPlan{}, false, err
	}
	for _, block := range plan.Blocks {
		item, ok := metadata[block.KnowledgePointID]
		if !ok {
			return PersistedPlan{}, false, fmt.Errorf("planner metadata missing for %s", block.KnowledgePointID)
		}
		var original any
		if block.OriginalTaskID != "" {
			parsed, err := uuid.Parse(block.OriginalTaskID)
			if err != nil {
				return PersistedPlan{}, false, err
			}
			original = parsed
		}
		blockID := uuid.New()
		if _, err := tx.Exec(ctx, `INSERT INTO learning_plan_blocks(id,plan_id,sequence,subject_id,knowledge_point_id,minutes,mode,reason,original_task_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, blockID, persisted.ID, block.Sequence, item.subjectID, item.knowledgePointID, block.Minutes, block.Mode, block.Reason, original); err != nil {
			return PersistedPlan{}, false, err
		}
		knowledgePointID := item.knowledgePointID
		var originalTaskID *uuid.UUID
		if original != nil {
			parsed := original.(uuid.UUID)
			originalTaskID = &parsed
		}
		persisted.Blocks = append(persisted.Blocks, PersistedBlock{ID: blockID, Sequence: block.Sequence, SubjectID: item.subjectID, SubjectCode: block.SubjectCode, KnowledgePointID: &knowledgePointID, Focus: item.focus, Minutes: block.Minutes, Mode: block.Mode, Reason: block.Reason, OriginalTaskID: originalTaskID, Status: "AVAILABLE"})
	}
	return persisted, true, nil
}

func (service *Service) ReplaceToday(ctx context.Context, studentID uuid.UUID, date time.Time) (PlanUpdate, error) {
	date = day(date)
	result := PlanUpdate{AppliesFrom: date}
	replanDate := date
	err := pgx.BeginFunc(ctx, service.pool, func(tx pgx.Tx) error {
		var lockedStudentID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM students WHERE id=$1 FOR NO KEY UPDATE`, studentID).Scan(&lockedStudentID); err != nil {
			return err
		}
		var activeSessions int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM learning_sessions WHERE student_id=$1 AND status IN ('ACTIVE','PAUSED')`, studentID).Scan(&activeSessions); err != nil {
			return err
		}
		var todayStarted bool
		if err := tx.QueryRow(ctx, `
SELECT EXISTS(
	SELECT 1
	FROM learning_plans plan
	JOIN learning_plan_blocks block ON block.plan_id=plan.id
	WHERE plan.student_id=$1 AND plan.plan_date=$2 AND plan.status IN ('PROPOSED','ACTIVE')
	  AND (block.status<>'AVAILABLE' OR EXISTS(SELECT 1 FROM learning_sessions session WHERE session.plan_block_id=block.id))
)`, studentID, date).Scan(&todayStarted); err != nil {
			return err
		}
		result.TodayPreserved = activeSessions > 0 || todayStarted
		if result.TodayPreserved {
			replanDate = date.AddDate(0, 0, 1)
			result.AppliesFrom = replanDate
		}
		// Completion creates tomorrow's plan ahead of time; replace future
		// proposals too so a new preference is not silently ignored. Once any
		// block has started, today's history remains stable.
		command, err := tx.Exec(ctx, `UPDATE learning_plans SET status='REPLACED' WHERE student_id=$1 AND plan_date>=$2 AND status IN ('PROPOSED','ACTIVE')`, studentID, replanDate)
		if err != nil {
			return err
		}
		replaced := command.RowsAffected() > 0
		if result.TodayPreserved && !replaced {
			return nil
		}
		_, created, ensureErr := service.ensureWithLocked(ctx, tx, studentID, replanDate, uuid.Nil)
		if ensureErr != nil {
			return ensureErr
		}
		result.PlanUpdated = replaced || created
		return nil
	})
	if err != nil {
		return PlanUpdate{}, err
	}
	return result, nil
}

type candidateMetadata struct {
	subjectID, knowledgePointID uuid.UUID
	focus                       string
}

func (service *Service) preferences(ctx context.Context, db queryer, studentID uuid.UUID) (Preferences, []string, []string, error) {
	preferences := Preferences{DailyMinutes: 30}
	var priorities, enabledSubjects []string
	err := db.QueryRow(ctx, `SELECT daily_minutes,review_only,reduce_intensity,priority_subject_codes,enabled_subject_codes FROM parent_preferences WHERE student_id=$1 ORDER BY updated_at DESC LIMIT 1`, studentID).Scan(&preferences.DailyMinutes, &preferences.ReviewOnly, &preferences.ReduceIntensity, &priorities, &enabledSubjects)
	if errors.Is(err, pgx.ErrNoRows) {
		return preferences, priorities, enabledSubjects, nil
	}
	return preferences, priorities, enabledSubjects, err
}

func (service *Service) candidates(ctx context.Context, db queryer, studentID uuid.UUID, date time.Time, priorities, enabledSubjects []string) ([]Candidate, map[string]candidateMetadata, error) {
	if enabledSubjects == nil {
		// A nil slice reaches PostgreSQL as NULL, and cardinality(NULL) is
		// NULL rather than 0, which would silently filter out everything.
		enabledSubjects = []string{}
	}
	rows, err := db.Query(ctx, `
SELECT s.id,s.code,kp.id,kp.name,COALESCE(ss.score_internal,0)::float8,
	   CASE WHEN EXISTS(SELECT 1 FROM review_queue rq WHERE rq.student_id=$1 AND rq.knowledge_point_id=kp.id AND rq.status='PENDING' AND rq.due_at < $2::timestamptz + interval '1 day') THEN $2::timestamptz ELSE NULL END,
       EXISTS(SELECT 1 FROM student_misconceptions sm WHERE sm.student_id=$1 AND sm.knowledge_point_id=kp.id AND sm.status='ACTIVE'),
	   COALESCE(s.code=ANY($3::text[]),false)
FROM knowledge_points kp JOIN subjects s ON s.id=kp.subject_id
LEFT JOIN student_skill_states ss ON ss.student_id=$1 AND ss.knowledge_point_id=kp.id
WHERE kp.status='RELEASED'
  AND EXISTS (SELECT 1 FROM questions q WHERE q.knowledge_point_id=kp.id AND q.status='RELEASED')
  AND (cardinality($4::text[])=0 OR s.code=ANY($4::text[]))
ORDER BY s.sort_order,kp.code`, studentID, date, priorities, enabledSubjects)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	candidates := []Candidate{}
	metadata := map[string]candidateMetadata{}
	indexes := map[string]int{}
	for rows.Next() {
		var subjectID, kpID uuid.UUID
		var subjectCode, focus string
		var score float64
		var due *time.Time
		var misconception, priority bool
		if err := rows.Scan(&subjectID, &subjectCode, &kpID, &focus, &score, &due, &misconception, &priority); err != nil {
			return nil, nil, err
		}
		key := kpID.String()
		indexes[key] = len(candidates)
		candidates = append(candidates, Candidate{SubjectCode: subjectCode, KnowledgePointID: key, SkillScore: score, ReviewDueAt: due, ActiveMisconception: misconception, ParentPriority: priority})
		metadata[key] = candidateMetadata{subjectID: subjectID, knowledgePointID: kpID, focus: focus}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	dependencyRows, err := db.Query(ctx, `
SELECT dep.source_knowledge_point_id,dep.target_knowledge_point_id
FROM cross_subject_dependencies dep
LEFT JOIN student_skill_states source_state ON source_state.student_id=$1 AND source_state.knowledge_point_id=dep.source_knowledge_point_id
LEFT JOIN student_skill_states target_state ON target_state.student_id=$1 AND target_state.knowledge_point_id=dep.target_knowledge_point_id
WHERE source_state.state IN('LEARNING','ASSISTED','REGRESSED','REVIEW_DUE')
  AND COALESCE(target_state.state,'UNKNOWN') NOT IN('UNDERSTOOD','MASTERED')
ORDER BY dep.strength DESC`, studentID)
	if err != nil {
		return nil, nil, err
	}
	defer dependencyRows.Close()
	for dependencyRows.Next() {
		var source, target uuid.UUID
		if err := dependencyRows.Scan(&source, &target); err != nil {
			return nil, nil, err
		}
		if index, ok := indexes[target.String()]; ok {
			candidates[index].CrossSubjectGap = true
			candidates[index].OriginalTaskID = source.String()
		}
	}
	return candidates, metadata, dependencyRows.Err()
}

func (service *Service) load(ctx context.Context, studentID uuid.UUID, date time.Time) (PersistedPlan, error) {
	return loadWith(ctx, service.pool, studentID, date)
}

type queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func loadWith(ctx context.Context, db queryer, studentID uuid.UUID, date time.Time) (PersistedPlan, error) {
	var plan PersistedPlan
	err := db.QueryRow(ctx, `SELECT id,student_id,plan_date,target_minutes FROM learning_plans WHERE student_id=$1 AND plan_date=$2 AND status IN('PROPOSED','ACTIVE') ORDER BY created_at DESC LIMIT 1`, studentID, date).Scan(&plan.ID, &plan.StudentID, &plan.Date, &plan.TargetMinutes)
	if err != nil {
		return plan, err
	}
	rows, err := db.Query(ctx, `
SELECT b.id,b.sequence,b.subject_id,s.code,b.knowledge_point_id,kp.name,b.minutes,b.mode,b.reason,b.original_task_id,b.status,
       open_session.id,open_session.status
FROM learning_plan_blocks b
JOIN subjects s ON s.id=b.subject_id
LEFT JOIN knowledge_points kp ON kp.id=b.knowledge_point_id
LEFT JOIN LATERAL (
    SELECT session.id,session.status FROM learning_sessions session
    WHERE session.plan_block_id=b.id
    ORDER BY session.started_at DESC LIMIT 1
) open_session ON true
WHERE b.plan_id=$1 ORDER BY b.sequence`, plan.ID)
	if err != nil {
		return plan, err
	}
	defer rows.Close()
	for rows.Next() {
		var block PersistedBlock
		if err := rows.Scan(&block.ID, &block.Sequence, &block.SubjectID, &block.SubjectCode, &block.KnowledgePointID, &block.Focus, &block.Minutes, &block.Mode, &block.Reason, &block.OriginalTaskID, &block.Status, &block.SessionID, &block.SessionStatus); err != nil {
			return plan, err
		}
		plan.Blocks = append(plan.Blocks, block)
	}
	return plan, rows.Err()
}
