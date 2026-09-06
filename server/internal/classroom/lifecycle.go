package classroom

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
	"github.com/oppositenum/ai-learning-tutor/server/internal/realtime"
)

const (
	stalePauseAfter   = 90 * time.Second
	staleAbandonAfter = 24 * time.Hour
	sessionLeaseTime  = 5 * time.Minute
)

type SessionTiming struct {
	SessionID            uuid.UUID  `json:"session_id"`
	Version              int64      `json:"version"`
	TimingVersion        int64      `json:"timing_version"`
	Status               string     `json:"status"`
	ActiveSeconds        int        `json:"active_seconds"`
	CurrentActiveSeconds int        `json:"current_active_seconds"`
	ActiveSince          *time.Time `json:"active_since,omitempty"`
	TimingObservedAt     time.Time  `json:"timing_observed_at"`
}

type lifecycleRow struct {
	sessionID          uuid.UUID
	studentID          uuid.UUID
	planBlockID        *uuid.UUID
	status             string
	startedAt          time.Time
	accumulatedSeconds int
	lastResumedAt      *time.Time
	lastActivityAt     time.Time
	version            int64
	timingVersion      int64
}

func (service *Service) PauseSession(ctx context.Context, userID, sessionID uuid.UUID) (SessionTiming, error) {
	if err := service.RecoverStaleSessions(ctx, userID); err != nil {
		return SessionTiming{}, err
	}
	return service.transitionSession(ctx, userID, sessionID, "PAUSE")
}

func (service *Service) ResumeSession(ctx context.Context, userID, sessionID uuid.UUID) (SessionTiming, error) {
	if err := service.RecoverStaleSessions(ctx, userID); err != nil {
		return SessionTiming{}, err
	}
	return service.transitionSession(ctx, userID, sessionID, "RESUME")
}

func (service *Service) AbandonSession(ctx context.Context, userID, sessionID uuid.UUID) (SessionTiming, error) {
	if err := service.RecoverStaleSessions(ctx, userID); err != nil {
		return SessionTiming{}, err
	}
	return service.transitionSession(ctx, userID, sessionID, "ABANDON")
}

func (service *Service) HeartbeatSession(ctx context.Context, userID, sessionID uuid.UUID) (SessionTiming, error) {
	if err := service.RecoverStaleSessions(ctx, userID); err != nil {
		return SessionTiming{}, err
	}
	return service.transitionSession(ctx, userID, sessionID, "HEARTBEAT")
}

func (service *Service) transitionSession(ctx context.Context, userID, sessionID uuid.UUID, action string) (SessionTiming, error) {
	var result SessionTiming
	var published *realtime.Event
	err := pgx.BeginFunc(ctx, service.pool, func(tx pgx.Tx) error {
		if err := auth.LockPrincipalSession(ctx, tx, userID); err != nil {
			return err
		}
		row, err := loadLifecycleRow(ctx, tx, userID, sessionID)
		if err != nil {
			return err
		}
		now := latestTime(service.now(), row.lastActivityAt)
		switch action {
		case "PAUSE":
			if row.status == "PAUSED" {
				result = timingFromRow(row, now)
				return nil
			}
			if row.status != "ACTIVE" {
				return ErrSessionNotActive
			}
			total := checkpointTotal(row, now)
			if _, err := tx.Exec(ctx, `UPDATE learning_sessions SET status='PAUSED',accumulated_seconds=$2,actual_seconds=$2,last_resumed_at=NULL,last_activity_at=$3,timing_version=timing_version+1 WHERE id=$1`, sessionID, total, now); err != nil {
				return err
			}
			row.timingVersion++
			row.status, row.accumulatedSeconds, row.lastResumedAt, row.lastActivityAt = "PAUSED", total, nil, now
			event, err := lifecycleEvent(ctx, tx, row, realtime.EventSessionPaused, "PAUSED", total, now)
			if err != nil {
				return err
			}
			published = &event
		case "RESUME":
			if row.status == "ACTIVE" {
				result = timingFromRow(row, now)
				return nil
			}
			if row.status != "PAUSED" {
				return ErrSessionNotActive
			}
			if _, err := tx.Exec(ctx, `UPDATE learning_sessions SET status='ACTIVE',last_resumed_at=$2,last_activity_at=$2,timing_version=timing_version+1 WHERE id=$1`, sessionID, now); err != nil {
				return err
			}
			row.timingVersion++
			row.status, row.lastResumedAt, row.lastActivityAt = "ACTIVE", &now, now
			event, err := lifecycleEvent(ctx, tx, row, realtime.EventSessionResumed, "ACTIVE", row.accumulatedSeconds, now)
			if err != nil {
				return err
			}
			published = &event
		case "ABANDON":
			if row.status == "ABANDONED" {
				result = timingFromRow(row, now)
				return nil
			}
			if row.status == "COMPLETED" {
				return ErrSessionNotActive
			}
			total := row.accumulatedSeconds
			if row.status == "ACTIVE" {
				total = checkpointTotal(row, now)
			}
			if _, err := tx.Exec(ctx, `UPDATE learning_sessions SET status='ABANDONED',ended_at=$2,accumulated_seconds=$3,actual_seconds=$3,last_resumed_at=NULL,last_activity_at=$2,processing_token=NULL,processing_until=NULL,version=version+1,timing_version=timing_version+1 WHERE id=$1`, sessionID, now, total); err != nil {
				return err
			}
			row.version++
			row.timingVersion++
			if row.planBlockID != nil {
				if _, err := tx.Exec(ctx, `UPDATE learning_plan_blocks SET status='AVAILABLE' WHERE id=$1 AND status='ACTIVE'`, *row.planBlockID); err != nil {
					return err
				}
			}
			row.status, row.accumulatedSeconds, row.lastResumedAt, row.lastActivityAt = "ABANDONED", total, nil, now
			event, err := lifecycleEvent(ctx, tx, row, realtime.EventSessionAbandoned, "ABANDONED", total, now)
			if err != nil {
				return err
			}
			published = &event
		case "HEARTBEAT":
			if row.status == "PAUSED" {
				result = timingFromRow(row, now)
				return nil
			}
			if row.status != "ACTIVE" {
				return ErrSessionNotActive
			}
			if _, err := tx.Exec(ctx, `UPDATE learning_sessions SET last_activity_at=$2,timing_version=timing_version+1 WHERE id=$1`, sessionID, now); err != nil {
				return err
			}
			row.timingVersion++
			row.lastActivityAt = now
		default:
			return errors.New("invalid lifecycle action")
		}
		result = timingFromRow(row, now)
		return nil
	})
	if err != nil {
		return SessionTiming{}, err
	}
	if published != nil && service.hub != nil {
		_ = service.hub.Publish(*published)
	}
	return result, nil
}

func (service *Service) RevokeAuthenticationAndPauseLearning(ctx context.Context, userID, authSessionID uuid.UUID) error {
	var published []realtime.Event
	err := pgx.BeginFunc(ctx, service.pool, func(tx pgx.Tx) error {
		now := service.now()
		command, err := tx.Exec(ctx, `
UPDATE sessions
SET revoked_at=$3
WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL AND expires_at>$3`, authSessionID, userID, now)
		if err != nil {
			return err
		}
		if command.RowsAffected() != 1 {
			return auth.ErrSessionRevoked
		}

		rows, err := tx.Query(ctx, `
SELECT ls.id,ls.student_id,ls.plan_block_id,ls.status,ls.started_at,ls.accumulated_seconds,
       ls.last_resumed_at,ls.last_activity_at,ls.version,ls.timing_version,
       ls.processing_token IS NOT NULL OR ls.processing_until IS NOT NULL
FROM learning_sessions ls
JOIN students student ON student.id=ls.student_id
WHERE student.user_id=$1 AND ls.status IN ('ACTIVE','PAUSED')
ORDER BY ls.started_at DESC
FOR UPDATE OF ls`, userID)
		if err != nil {
			return err
		}
		type logoutRow struct {
			lifecycleRow
			hasOperationLease bool
		}
		var sessions []logoutRow
		for rows.Next() {
			var session logoutRow
			if err := rows.Scan(
				&session.sessionID, &session.studentID, &session.planBlockID, &session.status, &session.startedAt,
				&session.accumulatedSeconds, &session.lastResumedAt, &session.lastActivityAt, &session.version,
				&session.timingVersion, &session.hasOperationLease,
			); err != nil {
				rows.Close()
				return err
			}
			sessions = append(sessions, session)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, session := range sessions {
			row := session.lifecycleRow
			if row.status == "PAUSED" {
				if session.hasOperationLease {
					if _, err := tx.Exec(ctx, `UPDATE learning_sessions SET processing_token=NULL,processing_until=NULL,version=version+1 WHERE id=$1`, row.sessionID); err != nil {
						return err
					}
				}
				continue
			}

			observedAt := latestTime(now, row.lastActivityAt)
			activeThrough := observedAt
			if observedAt.Sub(row.lastActivityAt) >= stalePauseAfter {
				activeThrough = row.lastActivityAt
			}
			total := checkpointTotal(row, activeThrough)
			if _, err := tx.Exec(ctx, `
UPDATE learning_sessions
SET status='PAUSED',accumulated_seconds=$2,actual_seconds=$2,last_resumed_at=NULL,
    last_activity_at=$3,processing_token=NULL,processing_until=NULL,
    version=version+1,timing_version=timing_version+1
WHERE id=$1`, row.sessionID, total, observedAt); err != nil {
				return err
			}
			row.version++
			row.timingVersion++
			row.status, row.accumulatedSeconds, row.lastResumedAt, row.lastActivityAt = "PAUSED", total, nil, observedAt
			event, err := lifecycleEvent(ctx, tx, row, realtime.EventSessionPaused, "PAUSED", total, observedAt)
			if err != nil {
				return err
			}
			published = append(published, event)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, event := range published {
		if service.hub != nil {
			_ = service.hub.Publish(event)
		}
	}
	return nil
}

func loadLifecycleRow(ctx context.Context, tx pgx.Tx, userID, sessionID uuid.UUID) (lifecycleRow, error) {
	var row lifecycleRow
	err := tx.QueryRow(ctx, `
SELECT ls.id,ls.student_id,ls.plan_block_id,ls.status,ls.started_at,ls.accumulated_seconds,
       ls.last_resumed_at,ls.last_activity_at,ls.version,ls.timing_version
FROM learning_sessions ls
JOIN students student ON student.id=ls.student_id
WHERE ls.id=$1 AND student.user_id=$2
FOR UPDATE OF ls`, sessionID, userID).Scan(
		&row.sessionID, &row.studentID, &row.planBlockID, &row.status, &row.startedAt,
		&row.accumulatedSeconds, &row.lastResumedAt, &row.lastActivityAt, &row.version, &row.timingVersion,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return row, ErrSessionNotFound
	}
	return row, err
}

func lifecycleEvent(ctx context.Context, tx pgx.Tx, row lifecycleRow, eventType realtime.EventType, status string, activeSeconds int, now time.Time) (realtime.Event, error) {
	_, sequence, err := nextSequences(ctx, tx, row.sessionID)
	if err != nil {
		return realtime.Event{}, err
	}
	studentPayload, _ := json.Marshal(map[string]any{"status": status, "active_seconds": activeSeconds})
	parentPayload, _ := json.Marshal(map[string]any{"status": status, "active_seconds": activeSeconds})
	event := makeEvent(row.studentID, row.sessionID, sequence, eventType, studentPayload, parentPayload, now)
	return event, insertEvent(ctx, tx, event)
}

func timingFromRow(row lifecycleRow, now time.Time) SessionTiming {
	current := 0
	var activeSince *time.Time
	if row.status == "ACTIVE" {
		start := row.startedAt
		if row.lastResumedAt != nil {
			start = *row.lastResumedAt
		}
		current = elapsedSeconds(start, now)
		activeSince = &start
	}
	return SessionTiming{
		SessionID:            row.sessionID,
		Version:              row.version,
		TimingVersion:        row.timingVersion,
		Status:               row.status,
		ActiveSeconds:        max(0, row.accumulatedSeconds+current),
		CurrentActiveSeconds: current,
		ActiveSince:          activeSince,
		TimingObservedAt:     now,
	}
}

func checkpointTotal(row lifecycleRow, end time.Time) int {
	if row.status != "ACTIVE" {
		return max(0, row.accumulatedSeconds)
	}
	start := row.startedAt
	if row.lastResumedAt != nil {
		start = *row.lastResumedAt
	}
	return max(0, row.accumulatedSeconds+elapsedSeconds(start, end))
}

func elapsedSeconds(start, end time.Time) int {
	if start.IsZero() || !end.After(start) {
		return 0
	}
	return int(end.Sub(start) / time.Second)
}

func (service *Service) RecoverStaleSessions(ctx context.Context, userID uuid.UUID) error {
	return service.recoverStaleSessions(ctx, &userID)
}

func (service *Service) RecoverAllStaleSessions(ctx context.Context) error {
	return service.recoverStaleSessions(ctx, nil)
}

func (service *Service) RunStaleSessionRecovery(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	recover := func() {
		if err := service.RecoverAllStaleSessions(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("recover stale classroom sessions: %v", err)
		}
	}
	recover()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			recover()
		}
	}
}

func (service *Service) recoverStaleSessions(ctx context.Context, userID *uuid.UUID) error {
	var published []realtime.Event
	err := pgx.BeginFunc(ctx, service.pool, func(tx pgx.Tx) error {
		if userID != nil {
			if err := auth.LockPrincipalSession(ctx, tx, *userID); err != nil {
				return err
			}
		}
		var userFilter any
		if userID != nil {
			userFilter = *userID
		}
		observedAt := service.now()
		rows, err := tx.Query(ctx, `
SELECT ls.id,ls.student_id,ls.plan_block_id,ls.status,ls.started_at,ls.accumulated_seconds,
       ls.last_resumed_at,ls.last_activity_at,ls.version,ls.timing_version
FROM learning_sessions ls
JOIN students student ON student.id=ls.student_id
WHERE ($1::uuid IS NULL OR student.user_id=$1)
  AND ((ls.status='ACTIVE' AND ls.last_activity_at<=$2::timestamptz-interval '90 seconds')
    OR (ls.status='PAUSED' AND ls.last_activity_at<=$2::timestamptz-interval '24 hours'))
  AND (ls.processing_until IS NULL OR ls.processing_until<=$2)
ORDER BY ls.started_at DESC
FOR UPDATE OF ls`, userFilter, observedAt)
		if err != nil {
			return err
		}
		var sessions []lifecycleRow
		for rows.Next() {
			var row lifecycleRow
			if err := rows.Scan(&row.sessionID, &row.studentID, &row.planBlockID, &row.status, &row.startedAt, &row.accumulatedSeconds, &row.lastResumedAt, &row.lastActivityAt, &row.version, &row.timingVersion); err != nil {
				rows.Close()
				return err
			}
			sessions = append(sessions, row)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		now := service.now()
		for _, row := range sessions {
			inactiveFor := now.Sub(row.lastActivityAt)
			if inactiveFor < stalePauseAfter {
				continue
			}
			status := "PAUSED"
			eventType := realtime.EventSessionPaused
			if inactiveFor >= staleAbandonAfter {
				status = "ABANDONED"
				eventType = realtime.EventSessionAbandoned
			}
			total := row.accumulatedSeconds
			if row.status == "ACTIVE" {
				total = checkpointTotal(row, row.lastActivityAt)
			}
			if status == "PAUSED" && row.status == "PAUSED" {
				continue
			}
			endedAt := any(nil)
			if status == "ABANDONED" {
				endedAt = now
			}
			versionIncrement := 0
			if status == "ABANDONED" {
				versionIncrement = 1
			}
			if _, err := tx.Exec(ctx, `UPDATE learning_sessions SET status=$2,ended_at=$3,accumulated_seconds=$4,actual_seconds=$4,last_resumed_at=NULL,processing_token=NULL,processing_until=NULL,version=version+$5,timing_version=timing_version+1 WHERE id=$1`, row.sessionID, status, endedAt, total, versionIncrement); err != nil {
				return err
			}
			row.version += int64(versionIncrement)
			row.timingVersion++
			if status == "ABANDONED" && row.planBlockID != nil {
				if _, err := tx.Exec(ctx, `UPDATE learning_plan_blocks SET status='AVAILABLE' WHERE id=$1 AND status='ACTIVE'`, *row.planBlockID); err != nil {
					return err
				}
			}
			row.status, row.accumulatedSeconds, row.lastResumedAt = status, total, nil
			event, err := lifecycleEvent(ctx, tx, row, eventType, status, total, now)
			if err != nil {
				return err
			}
			published = append(published, event)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, event := range published {
		if service.hub != nil {
			_ = service.hub.Publish(event)
		}
	}
	return nil
}

func latestTime(value, floor time.Time) time.Time {
	if value.Before(floor) {
		return floor
	}
	return value
}

func (service *Service) beginSessionOperation(ctx context.Context, userID, sessionID uuid.UUID) (uuid.UUID, error) {
	now := service.now()
	token := uuid.New()
	var changed bool
	err := pgx.BeginFunc(ctx, service.pool, func(tx pgx.Tx) error {
		if err := auth.LockPrincipalSession(ctx, tx, userID); err != nil {
			return err
		}
		command, err := tx.Exec(ctx, `
	UPDATE learning_sessions session
	SET processing_token=$3,processing_until=$4,last_activity_at=GREATEST(last_activity_at,$5)
	FROM students student
	WHERE session.id=$1 AND student.id=session.student_id AND student.user_id=$2
	  AND session.status='ACTIVE'
	  AND (session.processing_until IS NULL OR session.processing_until<=$5)`, sessionID, userID, token, now.Add(sessionLeaseTime), now)
		if err != nil {
			return err
		}
		changed = command.RowsAffected() == 1
		return nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	if !changed {
		return uuid.Nil, ErrClassroomChanged
	}
	return token, nil
}

func (service *Service) endSessionOperation(ctx context.Context, sessionID, token uuid.UUID) {
	cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, _ = service.pool.Exec(cleanupContext, `UPDATE learning_sessions SET processing_token=NULL,processing_until=NULL WHERE id=$1 AND processing_token=$2`, sessionID, token)
}
