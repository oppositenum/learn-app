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
	date = day(date)
	if existing, err := service.load(ctx, studentID, date); err == nil {
		return existing, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return PersistedPlan{}, err
	}

	preferences, prioritySubjects, enabledSubjects, err := service.preferences(ctx, studentID)
	if err != nil {
		return PersistedPlan{}, err
	}
	candidates, metadata, err := service.candidates(ctx, studentID, date, prioritySubjects, enabledSubjects)
	if err != nil {
		return PersistedPlan{}, err
	}
	if len(candidates) == 0 && len(enabledSubjects) > 0 {
		// Enabled subjects may have no released content yet; never let a
		// parent setting leave the student without a plan.
		candidates, metadata, err = service.candidates(ctx, studentID, date, prioritySubjects, nil)
		if err != nil {
			return PersistedPlan{}, err
		}
	}
	plan := service.engine.Build(Input{Date: date, Candidates: candidates, Preferences: preferences})
	if len(plan.Blocks) == 0 {
		return PersistedPlan{}, errors.New("no released curriculum candidates available")
	}

	persisted := PersistedPlan{ID: uuid.New(), StudentID: studentID, Date: plan.Date, TargetMinutes: plan.TargetMinutes}
	err = pgx.BeginFunc(ctx, service.pool, func(tx pgx.Tx) error {
		if existing, err := loadWith(ctx, tx, studentID, date); err == nil {
			persisted = existing
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var basedOn any
		if basedOnSessionID != uuid.Nil {
			basedOn = basedOnSessionID
		}
		if _, err := tx.Exec(ctx, `INSERT INTO learning_plans(id,student_id,plan_date,target_minutes,status,based_on_session_id) VALUES($1,$2,$3,$4,'PROPOSED',$5)`, persisted.ID, studentID, date, plan.TargetMinutes, basedOn); err != nil {
			return err
		}
		for _, block := range plan.Blocks {
			item, ok := metadata[block.KnowledgePointID]
			if !ok {
				return fmt.Errorf("planner metadata missing for %s", block.KnowledgePointID)
			}
			var original any
			if block.OriginalTaskID != "" {
				parsed, err := uuid.Parse(block.OriginalTaskID)
				if err != nil {
					return err
				}
				original = parsed
			}
			blockID := uuid.New()
			if _, err := tx.Exec(ctx, `INSERT INTO learning_plan_blocks(id,plan_id,sequence,subject_id,knowledge_point_id,minutes,mode,reason,original_task_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, blockID, persisted.ID, block.Sequence, item.subjectID, item.knowledgePointID, block.Minutes, block.Mode, block.Reason, original); err != nil {
				return err
			}
			knowledgePointID := item.knowledgePointID
			var originalTaskID *uuid.UUID
			if original != nil {
				parsed := original.(uuid.UUID)
				originalTaskID = &parsed
			}
			persisted.Blocks = append(persisted.Blocks, PersistedBlock{ID: blockID, Sequence: block.Sequence, SubjectID: item.subjectID, SubjectCode: block.SubjectCode, KnowledgePointID: &knowledgePointID, Focus: item.focus, Minutes: block.Minutes, Mode: block.Mode, Reason: block.Reason, OriginalTaskID: originalTaskID})
		}
		return nil
	})
	return persisted, err
}

func (service *Service) ReplaceToday(ctx context.Context, studentID uuid.UUID, date time.Time) (PersistedPlan, bool, error) {
	date = day(date)
	var activeSessions int
	if err := service.pool.QueryRow(ctx, `SELECT count(*) FROM learning_sessions WHERE student_id=$1 AND status='ACTIVE'`, studentID).Scan(&activeSessions); err != nil {
		return PersistedPlan{}, false, err
	}
	if activeSessions > 0 {
		plan, err := service.load(ctx, studentID, date)
		return plan, false, err
	}
	// Also invalidate pre-generated future plans (session completion creates
	// tomorrow's plan ahead of time) so new preferences are not ignored there.
	if _, err := service.pool.Exec(ctx, `UPDATE learning_plans SET status='REPLACED' WHERE student_id=$1 AND plan_date>=$2 AND status='PROPOSED'`, studentID, date); err != nil {
		return PersistedPlan{}, false, err
	}
	plan, err := service.Ensure(ctx, studentID, date, uuid.Nil)
	return plan, err == nil, err
}

type candidateMetadata struct {
	subjectID, knowledgePointID uuid.UUID
	focus                       string
}

func (service *Service) preferences(ctx context.Context, studentID uuid.UUID) (Preferences, []string, []string, error) {
	preferences := Preferences{DailyMinutes: 30}
	var priorities, enabledSubjects []string
	err := service.pool.QueryRow(ctx, `SELECT daily_minutes,review_only,reduce_intensity,priority_subject_codes,enabled_subject_codes FROM parent_preferences WHERE student_id=$1 ORDER BY updated_at DESC LIMIT 1`, studentID).Scan(&preferences.DailyMinutes, &preferences.ReviewOnly, &preferences.ReduceIntensity, &priorities, &enabledSubjects)
	if errors.Is(err, pgx.ErrNoRows) {
		return preferences, priorities, enabledSubjects, nil
	}
	return preferences, priorities, enabledSubjects, err
}

func (service *Service) candidates(ctx context.Context, studentID uuid.UUID, date time.Time, priorities, enabledSubjects []string) ([]Candidate, map[string]candidateMetadata, error) {
	if enabledSubjects == nil {
		// A nil slice reaches PostgreSQL as NULL, and cardinality(NULL) is
		// NULL rather than 0, which would silently filter out everything.
		enabledSubjects = []string{}
	}
	rows, err := service.pool.Query(ctx, `
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
	dependencyRows, err := service.pool.Query(ctx, `
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
	rows, err := db.Query(ctx, `SELECT b.id,b.sequence,b.subject_id,s.code,b.knowledge_point_id,kp.name,b.minutes,b.mode,b.reason,b.original_task_id FROM learning_plan_blocks b JOIN subjects s ON s.id=b.subject_id LEFT JOIN knowledge_points kp ON kp.id=b.knowledge_point_id WHERE b.plan_id=$1 ORDER BY b.sequence`, plan.ID)
	if err != nil {
		return plan, err
	}
	defer rows.Close()
	for rows.Next() {
		var block PersistedBlock
		if err := rows.Scan(&block.ID, &block.Sequence, &block.SubjectID, &block.SubjectCode, &block.KnowledgePointID, &block.Focus, &block.Minutes, &block.Mode, &block.Reason, &block.OriginalTaskID); err != nil {
			return plan, err
		}
		plan.Blocks = append(plan.Blocks, block)
	}
	return plan, rows.Err()
}
