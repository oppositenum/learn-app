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

type SubjectAvailabilityError struct {
	AvailableSubjectCodes []string
}

func (err *SubjectAvailabilityError) Error() string {
	if len(err.AvailableSubjectCodes) == 0 {
		return "no released curriculum candidates available for this grade band"
	}
	return "enabled subjects have no released curriculum candidates for this grade band"
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
	asOf := date
	date = day(date)
	var persisted PersistedPlan
	created := false
	err := pgx.BeginFunc(ctx, service.pool, func(tx pgx.Tx) error {
		var lockedStudentID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM students WHERE id=$1 FOR NO KEY UPDATE`, studentID).Scan(&lockedStudentID); err != nil {
			return err
		}
		var err error
		persisted, created, err = service.ensureWithLocked(ctx, tx, studentID, date, asOf, basedOnSessionID)
		return err
	})
	return persisted, created && err == nil, err
}

func (service *Service) ensureWithLocked(ctx context.Context, tx pgx.Tx, studentID uuid.UUID, date, asOf time.Time, basedOnSessionID uuid.UUID) (PersistedPlan, bool, error) {
	if existing, err := loadWith(ctx, tx, studentID, date); err == nil {
		refreshed, err := service.refreshUnusedBlocksWithLocked(ctx, tx, studentID, date, asOf, existing)
		if err != nil {
			return PersistedPlan{}, false, err
		}
		return refreshed, false, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return PersistedPlan{}, false, err
	}
	plan, metadata, err := service.buildPlanWithLocked(ctx, tx, studentID, asOf)
	if err != nil {
		return PersistedPlan{}, false, err
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
		var reviewQueueID any
		if block.Mode == ModeReview {
			if item.reviewQueueID != nil {
				reviewQueueID = *item.reviewQueueID
			} else {
				queueID := uuid.New()
				if err := tx.QueryRow(ctx, `
INSERT INTO review_queue(id,student_id,knowledge_point_id,source,due_at,priority)
VALUES($1,$2,$3,'PARENT_PRIORITY',$4,70)
ON CONFLICT(student_id,knowledge_point_id,source) WHERE status='PENDING'
DO UPDATE SET due_at=LEAST(review_queue.due_at,EXCLUDED.due_at),priority=GREATEST(review_queue.priority,EXCLUDED.priority)
					RETURNING id`, queueID, studentID, item.knowledgePointID, asOf).Scan(&queueID); err != nil {
					return PersistedPlan{}, false, err
				}
				reviewQueueID = queueID
			}
		}
		blockID := uuid.New()
		if _, err := tx.Exec(ctx, `INSERT INTO learning_plan_blocks(id,plan_id,sequence,subject_id,knowledge_point_id,minutes,mode,reason,original_task_id,review_queue_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, blockID, persisted.ID, block.Sequence, item.subjectID, item.knowledgePointID, block.Minutes, block.Mode, block.Reason, original, reviewQueueID); err != nil {
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

// refreshUnusedBlocksWithLocked may replace AVAILABLE cards that are not in
// an ACTIVE or PAUSED classroom. Completed work and an in-progress or paused
// card stay as the child last saw them.
func (service *Service) refreshUnusedBlocksWithLocked(ctx context.Context, tx pgx.Tx, studentID uuid.UUID, date, asOf time.Time, existing PersistedPlan) (PersistedPlan, error) {
	newPlan, metadata, err := service.buildPlanWithLocked(ctx, tx, studentID, asOf)
	if err != nil {
		return existing, err
	}
	lockedSubjects := map[string]bool{}
	existingSubjects := map[string]bool{}
	nextSequence := 0
	for _, block := range existing.Blocks {
		existingSubjects[block.SubjectCode] = true
		if block.Sequence > nextSequence {
			nextSequence = block.Sequence
		}
		if sessionLocksTodayCard(block.SessionStatus) {
			lockedSubjects[block.SubjectCode] = true
		}
	}
	changed := false
	for _, block := range existing.Blocks {
		if lockedSubjects[block.SubjectCode] {
			continue
		}
		replacement, ok := blockForSubject(newPlan.Blocks, block.SubjectCode)
		if !ok {
			continue
		}
		item, ok := metadata[replacement.KnowledgePointID]
		if !ok {
			return existing, fmt.Errorf("planner metadata missing for %s", replacement.KnowledgePointID)
		}
		if block.KnowledgePointID != nil && block.KnowledgePointID.String() == replacement.KnowledgePointID {
			continue
		}
		var original any
		if replacement.OriginalTaskID != "" {
			parsed, err := uuid.Parse(replacement.OriginalTaskID)
			if err != nil {
				return existing, err
			}
			original = parsed
		}
		var reviewQueueID any
		if replacement.Mode == ModeReview && item.reviewQueueID != nil {
			reviewQueueID = *item.reviewQueueID
		}
		command, err := tx.Exec(ctx, `UPDATE learning_plan_blocks SET knowledge_point_id=$2,minutes=$3,mode=$4,reason=$5,original_task_id=$6,review_queue_id=$7 WHERE id=$1 AND status='AVAILABLE'`, block.ID, item.knowledgePointID, replacement.Minutes, replacement.Mode, replacement.Reason, original, reviewQueueID)
		if err != nil {
			return existing, err
		}
		if command.RowsAffected() == 0 {
			continue
		}
		changed = true
	}
	for _, replacement := range newPlan.Blocks {
		if existingSubjects[replacement.SubjectCode] || lockedSubjects[replacement.SubjectCode] {
			continue
		}
		item, ok := metadata[replacement.KnowledgePointID]
		if !ok {
			return existing, fmt.Errorf("planner metadata missing for %s", replacement.KnowledgePointID)
		}
		var original any
		if replacement.OriginalTaskID != "" {
			parsed, err := uuid.Parse(replacement.OriginalTaskID)
			if err != nil {
				return existing, err
			}
			original = parsed
		}
		var reviewQueueID any
		if replacement.Mode == ModeReview && item.reviewQueueID != nil {
			reviewQueueID = *item.reviewQueueID
		}
		nextSequence++
		if _, err := tx.Exec(ctx, `INSERT INTO learning_plan_blocks(id,plan_id,sequence,subject_id,knowledge_point_id,minutes,mode,reason,original_task_id,review_queue_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, uuid.New(), existing.ID, nextSequence, item.subjectID, item.knowledgePointID, replacement.Minutes, replacement.Mode, replacement.Reason, original, reviewQueueID); err != nil {
			return existing, err
		}
		changed = true
	}
	if !changed {
		return existing, nil
	}
	return loadWith(ctx, tx, studentID, date)
}

func blockForSubject(blocks []Block, subjectCode string) (Block, bool) {
	for _, block := range blocks {
		if block.SubjectCode == subjectCode {
			return block, true
		}
	}
	return Block{}, false
}

func (service *Service) buildPlanWithLocked(ctx context.Context, tx pgx.Tx, studentID uuid.UUID, asOf time.Time) (Plan, map[string]candidateMetadata, error) {
	preferences, prioritySubjects, enabledSubjects, err := service.preferences(ctx, tx, studentID)
	if err != nil {
		return Plan{}, nil, err
	}
	candidates, metadata, err := service.candidates(ctx, tx, studentID, asOf, prioritySubjects, enabledSubjects)
	if err != nil {
		return Plan{}, nil, err
	}
	plan := service.engine.Build(Input{Date: asOf, Candidates: candidates, Preferences: preferences})
	if len(plan.Blocks) == 0 && len(enabledSubjects) > 0 {
		allCandidates, _, candidateErr := service.candidates(ctx, tx, studentID, asOf, prioritySubjects, nil)
		if candidateErr != nil {
			return Plan{}, nil, candidateErr
		}
		availablePlan := service.engine.Build(Input{Date: asOf, Candidates: allCandidates, Preferences: preferences})
		availableSubjects := make([]string, 0, len(availablePlan.Blocks))
		for _, block := range availablePlan.Blocks {
			availableSubjects = append(availableSubjects, block.SubjectCode)
		}
		return Plan{}, nil, &SubjectAvailabilityError{AvailableSubjectCodes: availableSubjects}
	}
	if len(plan.Blocks) == 0 {
		return Plan{}, nil, errors.New("no released curriculum candidates available")
	}
	return plan, metadata, nil
}

func (service *Service) ReplaceToday(ctx context.Context, studentID uuid.UUID, date time.Time) (PlanUpdate, error) {
	asOf := date
	date = day(date)
	result := PlanUpdate{AppliesFrom: date}
	replanDate := date
	err := pgx.BeginFunc(ctx, service.pool, func(tx pgx.Tx) error {
		var lockedStudentID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM students WHERE id=$1 FOR NO KEY UPDATE`, studentID).Scan(&lockedStudentID); err != nil {
			return err
		}
		var activeSessions int
		if err := tx.QueryRow(ctx, `
	SELECT count(*)
	FROM learning_sessions session
	JOIN learning_plan_blocks block ON block.id=session.plan_block_id
	JOIN learning_plans plan ON plan.id=block.plan_id AND plan.student_id=session.student_id
	WHERE session.student_id=$1 AND session.status IN ('ACTIVE','PAUSED') AND plan.plan_date=$2`, studentID, date).Scan(&activeSessions); err != nil {
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
		result.TodayPreserved = shouldPreserveToday(activeSessions, todayStarted)
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
			_, _, availabilityErr := service.buildPlanWithLocked(ctx, tx, studentID, asOf.AddDate(0, 0, 1))
			return availabilityErr
		}
		replanAsOf := asOf
		if result.TodayPreserved {
			replanAsOf = asOf.AddDate(0, 0, 1)
		}
		_, created, ensureErr := service.ensureWithLocked(ctx, tx, studentID, replanDate, replanAsOf, uuid.Nil)
		if ensureErr != nil {
			return ensureErr
		}
		result.PlanUpdated = replaced || created
		return nil
	})
	if err != nil {
		var availability *SubjectAvailabilityError
		if errors.As(err, &availability) {
			return result, err
		}
		return PlanUpdate{}, err
	}
	return result, nil
}

func shouldPreserveToday(openTodaySessions int, todayStarted bool) bool {
	return openTodaySessions > 0 || todayStarted
}

func sessionLocksTodayCard(sessionStatus *string) bool {
	if sessionStatus == nil {
		return false
	}
	switch *sessionStatus {
	case "ACTIVE", "PAUSED":
		return true
	default:
		return false
	}
}

type candidateMetadata struct {
	subjectID, knowledgePointID uuid.UUID
	focus                       string
	reviewQueueID               *uuid.UUID
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
		SELECT s.id,s.code,kp.id,kp.code,kp.name,st.grade_level,grade_band.min_grade,grade_band.max_grade,
			   COALESCE(ss.score_internal,0)::float8,due_review.id,due_review.due_at,
	       EXISTS(SELECT 1 FROM student_misconceptions sm WHERE sm.student_id=$1 AND sm.knowledge_point_id=kp.id AND sm.status='ACTIVE'),
		   COALESCE(s.code=ANY($3::text[]),false),
		   ss.student_id IS NOT NULL
	FROM knowledge_points kp
	JOIN subjects s ON s.id=kp.subject_id
	JOIN students st ON st.id=$1
		JOIN grade_bands grade_band ON grade_band.code=kp.grade_band_code
		LEFT JOIN student_skill_states ss ON ss.student_id=$1 AND ss.knowledge_point_id=kp.id
		LEFT JOIN LATERAL (
			SELECT rq.id,rq.due_at
			FROM review_queue rq
			WHERE rq.student_id=$1 AND rq.knowledge_point_id=kp.id
			  AND rq.status='PENDING' AND rq.due_at<=$2
			ORDER BY rq.due_at,rq.priority DESC,rq.created_at,rq.id
			LIMIT 1
		) due_review ON true
	WHERE kp.status='RELEASED'
	  AND grade_band.min_grade<=st.grade_level
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
		var reviewQueueID *uuid.UUID
		var subjectCode, knowledgePointCode, focus string
		var studentGrade, gradeBandMin, gradeBandMax int
		var score float64
		var due *time.Time
		var misconception, priority, practiced bool
		if err := rows.Scan(&subjectID, &subjectCode, &kpID, &knowledgePointCode, &focus, &studentGrade, &gradeBandMin, &gradeBandMax, &score, &reviewQueueID, &due, &misconception, &priority, &practiced); err != nil {
			return nil, nil, err
		}
		key := kpID.String()
		indexes[key] = len(candidates)
		candidates = append(candidates, Candidate{SubjectCode: subjectCode, KnowledgePointID: key, StudentGrade: studentGrade, GradeBandMin: gradeBandMin, GradeBandMax: gradeBandMax, SkillScore: score, FoundationPriority: foundationPriority(subjectCode, knowledgePointCode), ReviewDueAt: due, ActiveMisconception: misconception, ParentPriority: priority, Practiced: practiced, RemoveParentheses: knowledgePointCode == "MATH-JUN-REMOVE-PARENTHESES"})
		metadata[key] = candidateMetadata{subjectID: subjectID, knowledgePointID: kpID, focus: focus, reviewQueueID: reviewQueueID}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	dependencyRows, err := db.Query(ctx, `
	SELECT dep.source_knowledge_point_id,dep.target_knowledge_point_id
	FROM cross_subject_dependencies dep
	JOIN knowledge_points source_kp ON source_kp.id=dep.source_knowledge_point_id AND source_kp.status='RELEASED'
	JOIN grade_bands source_grade ON source_grade.code=source_kp.grade_band_code
	JOIN students st ON st.id=$1
	LEFT JOIN student_skill_states source_state ON source_state.student_id=$1 AND source_state.knowledge_point_id=dep.source_knowledge_point_id
LEFT JOIN student_skill_states target_state ON target_state.student_id=$1 AND target_state.knowledge_point_id=dep.target_knowledge_point_id
	WHERE source_state.state IN('LEARNING','ASSISTED','REGRESSED','REVIEW_DUE')
	  AND source_grade.min_grade<=st.grade_level
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
