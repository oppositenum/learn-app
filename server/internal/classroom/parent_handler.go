package classroom

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
	"github.com/oppositenum/ai-learning-tutor/server/internal/realtime"
)

type parentSubjectAbility struct {
	Code         string  `json:"code"`
	Name         string  `json:"name"`
	Started      int     `json:"started_knowledge_points"`
	Understood   int     `json:"understood_knowledge_points"`
	Mastered     int     `json:"mastered_knowledge_points"`
	AverageScore float64 `json:"average_score"`
}

type parentCoreAbility struct {
	Code          string  `json:"code"`
	Name          string  `json:"name"`
	Score         float64 `json:"score"`
	EvidenceCount int     `json:"evidence_count"`
}

type parentMisconception struct {
	Code                  string    `json:"code"`
	Name                  string    `json:"name"`
	Subject               string    `json:"subject"`
	KnowledgePoint        string    `json:"knowledge_point"`
	Occurrences           int       `json:"occurrences"`
	SuccessfulCorrections int       `json:"successful_corrections"`
	Status                string    `json:"status"`
	LastSeenAt            time.Time `json:"last_seen_at"`
}

type parentActivityDay struct {
	Date              string `json:"date"`
	CompletedSessions int    `json:"completed_sessions"`
	ActiveSeconds     int    `json:"active_seconds"`
}

type parentRecentSession struct {
	ID             uuid.UUID  `json:"id"`
	Subject        string     `json:"subject"`
	KnowledgePoint string     `json:"knowledge_point"`
	Status         string     `json:"status"`
	State          string     `json:"state"`
	StartedAt      time.Time  `json:"started_at"`
	EndedAt        *time.Time `json:"ended_at,omitempty"`
	ActiveSeconds  int        `json:"active_seconds"`
}

type parentSafetyEvent struct {
	ID            uuid.UUID `json:"id"`
	PolicyVersion string    `json:"policy_version"`
	Category      string    `json:"category"`
	Severity      string    `json:"severity"`
	FixedAction   string    `json:"fixed_action"`
	OccurredAt    time.Time `json:"occurred_at"`
}

func (handler *Handler) ParentChildren(writer http.ResponseWriter, request *http.Request) {
	principal, _ := auth.PrincipalFromContext(request.Context())
	parentID, err := uuid.Parse(principal.UserID)
	if err != nil {
		http.Error(writer, "invalid principal", http.StatusUnauthorized)
		return
	}
	rows, err := handler.pool.Query(request.Context(), `
SELECT st.id,u.display_name,st.grade_level,ls.id,s.name_zh,kp.name,ls.started_at
FROM parent_student_links l JOIN students st ON st.id=l.student_id JOIN users u ON u.id=st.user_id
	LEFT JOIN LATERAL (SELECT * FROM learning_sessions x WHERE x.student_id=st.id AND x.status='ACTIVE' AND x.last_activity_at>=CURRENT_TIMESTAMP-interval '90 seconds' ORDER BY x.started_at DESC LIMIT 1) ls ON true
LEFT JOIN subjects s ON s.id=ls.subject_id LEFT JOIN questions q ON q.id=ls.current_question_id LEFT JOIN knowledge_points kp ON kp.id=q.knowledge_point_id
WHERE l.parent_user_id=$1 AND l.status='ACTIVE' ORDER BY l.created_at`, parentID)
	if err != nil {
		http.Error(writer, "children unavailable", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	records := []map[string]any{}
	for rows.Next() {
		var studentID uuid.UUID
		var name string
		var grade int
		var sessionID *uuid.UUID
		var subject, knowledge *string
		var startedAt *time.Time
		if err := rows.Scan(&studentID, &name, &grade, &sessionID, &subject, &knowledge, &startedAt); err != nil {
			http.Error(writer, "children unavailable", http.StatusInternalServerError)
			return
		}
		records = append(records, map[string]any{"student_id": studentID, "display_name": name, "grade_level": grade, "active_session_id": sessionID, "subject": subject, "knowledge_point": knowledge, "started_at": startedAt})
	}
	writeJSON(writer, http.StatusOK, map[string]any{"children": records})
}

func (handler *Handler) ParentAbility(writer http.ResponseWriter, request *http.Request) {
	studentID, ok := handler.authorizedParentStudent(writer, request)
	if !ok {
		return
	}
	rows, err := handler.pool.Query(request.Context(), `
SELECT s.code,s.name_zh,
       count(ss.knowledge_point_id)::int,
       count(*) FILTER (WHERE ss.state IN('UNDERSTOOD','MASTERED'))::int,
       count(*) FILTER (WHERE ss.state='MASTERED')::int,
       COALESCE(avg(ss.score_internal),0)::float8
FROM subjects s
LEFT JOIN knowledge_points kp ON kp.subject_id=s.id AND kp.status='RELEASED'
LEFT JOIN student_skill_states ss ON ss.knowledge_point_id=kp.id AND ss.student_id=$1
GROUP BY s.id,s.code,s.name_zh,s.sort_order ORDER BY s.sort_order`, studentID)
	if err != nil {
		http.Error(writer, "ability unavailable", http.StatusInternalServerError)
		return
	}
	subjects := []parentSubjectAbility{}
	for rows.Next() {
		var item parentSubjectAbility
		if err := rows.Scan(&item.Code, &item.Name, &item.Started, &item.Understood, &item.Mastered, &item.AverageScore); err != nil {
			rows.Close()
			http.Error(writer, "ability unavailable", http.StatusInternalServerError)
			return
		}
		subjects = append(subjects, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		http.Error(writer, "ability unavailable", http.StatusInternalServerError)
		return
	}
	rows.Close()

	abilityRows, err := handler.pool.Query(request.Context(), `
SELECT ca.code,ca.name,COALESCE(sa.score_internal,0)::float8,COALESCE(sa.evidence_count,0)::int
FROM core_abilities ca LEFT JOIN student_ability_states sa ON sa.core_ability_id=ca.id AND sa.student_id=$1
ORDER BY ca.code`, studentID)
	if err != nil {
		http.Error(writer, "ability unavailable", http.StatusInternalServerError)
		return
	}
	abilities := []parentCoreAbility{}
	for abilityRows.Next() {
		var item parentCoreAbility
		if err := abilityRows.Scan(&item.Code, &item.Name, &item.Score, &item.EvidenceCount); err != nil {
			abilityRows.Close()
			http.Error(writer, "ability unavailable", http.StatusInternalServerError)
			return
		}
		abilities = append(abilities, item)
	}
	if err := abilityRows.Err(); err != nil {
		abilityRows.Close()
		http.Error(writer, "ability unavailable", http.StatusInternalServerError)
		return
	}
	abilityRows.Close()

	misconceptionRows, err := handler.pool.Query(request.Context(), `
SELECT m.code,m.name,s.name_zh,kp.name,sm.occurrences,sm.successful_corrections,sm.status,sm.last_seen_at
FROM student_misconceptions sm
JOIN misconceptions m ON m.id=sm.misconception_id
JOIN knowledge_points kp ON kp.id=sm.knowledge_point_id
JOIN subjects s ON s.id=kp.subject_id
WHERE sm.student_id=$1 AND sm.status<>'RESOLVED'
ORDER BY sm.last_seen_at DESC,sm.occurrences DESC LIMIT 10`, studentID)
	if err != nil {
		http.Error(writer, "ability unavailable", http.StatusInternalServerError)
		return
	}
	misconceptions := []parentMisconception{}
	for misconceptionRows.Next() {
		var item parentMisconception
		if err := misconceptionRows.Scan(&item.Code, &item.Name, &item.Subject, &item.KnowledgePoint, &item.Occurrences, &item.SuccessfulCorrections, &item.Status, &item.LastSeenAt); err != nil {
			misconceptionRows.Close()
			http.Error(writer, "ability unavailable", http.StatusInternalServerError)
			return
		}
		misconceptions = append(misconceptions, item)
	}
	if err := misconceptionRows.Err(); err != nil {
		misconceptionRows.Close()
		http.Error(writer, "ability unavailable", http.StatusInternalServerError)
		return
	}
	misconceptionRows.Close()
	writeJSON(writer, http.StatusOK, map[string]any{"student_id": studentID, "subjects": subjects, "core_abilities": abilities, "misconceptions": misconceptions})
}

func (handler *Handler) ParentReport(writer http.ResponseWriter, request *http.Request) {
	studentID, ok := handler.authorizedParentStudent(writer, request)
	if !ok {
		return
	}
	var completedSessions, activeSeconds, totalEnergy, rewardEvents int
	if err := handler.pool.QueryRow(request.Context(), `
	SELECT count(*) FILTER(WHERE ls.status='COMPLETED')::int,
	       COALESCE(sum(ls.actual_seconds) FILTER(WHERE ls.status='COMPLETED'),0)::int,
	       COALESCE(g.total_energy,0)::int,
	       (SELECT count(*)::int FROM reward_events re WHERE re.student_id=st.id)
FROM students st
LEFT JOIN learning_sessions ls ON ls.student_id=st.id
LEFT JOIN student_growth g ON g.student_id=st.id
	WHERE st.id=$1 GROUP BY st.id,g.total_energy`, studentID).Scan(&completedSessions, &activeSeconds, &totalEnergy, &rewardEvents); err != nil {
		http.Error(writer, "report unavailable", http.StatusInternalServerError)
		return
	}
	streakDays, err := currentStreak(request.Context(), handler.pool, studentID, handler.now())
	if err != nil {
		http.Error(writer, "report unavailable", http.StatusInternalServerError)
		return
	}
	dayRows, err := handler.pool.Query(request.Context(), `SELECT activity_date,completed_sessions,active_seconds FROM student_activity_days WHERE student_id=$1 ORDER BY activity_date DESC LIMIT 14`, studentID)
	if err != nil {
		http.Error(writer, "report unavailable", http.StatusInternalServerError)
		return
	}
	days := []parentActivityDay{}
	for dayRows.Next() {
		var date time.Time
		var day parentActivityDay
		if err := dayRows.Scan(&date, &day.CompletedSessions, &day.ActiveSeconds); err != nil {
			dayRows.Close()
			http.Error(writer, "report unavailable", http.StatusInternalServerError)
			return
		}
		day.Date = date.Format("2006-01-02")
		days = append(days, day)
	}
	if err := dayRows.Err(); err != nil {
		dayRows.Close()
		http.Error(writer, "report unavailable", http.StatusInternalServerError)
		return
	}
	dayRows.Close()

	sessionRows, err := handler.pool.Query(request.Context(), `
	SELECT ls.id,s.name_zh,kp.name,ls.status,ls.current_state,ls.started_at,ls.ended_at,
		       ls.accumulated_seconds + CASE WHEN ls.status='ACTIVE' THEN GREATEST(0,EXTRACT(EPOCH FROM ((CASE WHEN ls.last_activity_at >= $2::timestamptz-interval '90 seconds' THEN $2::timestamptz ELSE ls.last_activity_at END)-COALESCE(ls.last_resumed_at,ls.started_at)))::integer) ELSE 0 END
FROM learning_sessions ls JOIN subjects s ON s.id=ls.subject_id
LEFT JOIN questions q ON q.id=ls.current_question_id LEFT JOIN knowledge_points kp ON kp.id=q.knowledge_point_id
	WHERE ls.student_id=$1 ORDER BY ls.started_at DESC LIMIT 10`, studentID, handler.now())
	if err != nil {
		http.Error(writer, "report unavailable", http.StatusInternalServerError)
		return
	}
	sessions := []parentRecentSession{}
	for sessionRows.Next() {
		var item parentRecentSession
		if err := sessionRows.Scan(&item.ID, &item.Subject, &item.KnowledgePoint, &item.Status, &item.State, &item.StartedAt, &item.EndedAt, &item.ActiveSeconds); err != nil {
			sessionRows.Close()
			http.Error(writer, "report unavailable", http.StatusInternalServerError)
			return
		}
		sessions = append(sessions, item)
	}
	if err := sessionRows.Err(); err != nil {
		sessionRows.Close()
		http.Error(writer, "report unavailable", http.StatusInternalServerError)
		return
	}
	sessionRows.Close()
	writeJSON(writer, http.StatusOK, map[string]any{
		"student_id":      studentID,
		"summary":         map[string]any{"completed_sessions": completedSessions, "active_seconds": activeSeconds, "total_energy": totalEnergy, "streak_days": streakDays, "reward_events": rewardEvents},
		"activity_days":   days,
		"recent_sessions": sessions,
	})
}

func (handler *Handler) ParentSafetyEvents(writer http.ResponseWriter, request *http.Request) {
	studentID, ok := handler.authorizedParentStudent(writer, request)
	if !ok {
		return
	}
	principal, _ := auth.PrincipalFromContext(request.Context())
	parentID, err := uuid.Parse(principal.UserID)
	if err != nil {
		http.Error(writer, "invalid principal", http.StatusUnauthorized)
		return
	}
	events := []parentSafetyEvent{}
	err = pgx.BeginFunc(request.Context(), handler.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(request.Context(), `
SELECT id,policy_version,category,severity,fixed_action,created_at
FROM minor_safety_incidents
WHERE student_id=$1 AND parent_escalated
ORDER BY created_at DESC,id DESC
LIMIT 50`, studentID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item parentSafetyEvent
			if err := rows.Scan(&item.ID, &item.PolicyVersion, &item.Category, &item.Severity, &item.FixedAction, &item.OccurredAt); err != nil {
				return err
			}
			events = append(events, item)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for _, item := range events {
			if _, err := tx.Exec(request.Context(), `
INSERT INTO minor_safety_access_audits(id,incident_id,accessor_user_id,channel)
VALUES($1,$2,$3,'PARENT_SUMMARY_API')`, uuid.New(), item.ID, parentID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		http.Error(writer, "safety events unavailable", http.StatusInternalServerError)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"student_id": studentID, "events": events})
}

func (handler *Handler) authorizedParentStudent(writer http.ResponseWriter, request *http.Request) (uuid.UUID, bool) {
	principal, _ := auth.PrincipalFromContext(request.Context())
	parentID, err := uuid.Parse(principal.UserID)
	if err != nil {
		http.Error(writer, "invalid principal", http.StatusUnauthorized)
		return uuid.Nil, false
	}
	studentID, err := uuid.Parse(request.PathValue("student_id"))
	if err != nil {
		http.Error(writer, "invalid student id", http.StatusBadRequest)
		return uuid.Nil, false
	}
	allowed, err := handler.parents.CanSupervise(request.Context(), parentID, studentID)
	if err != nil || !allowed {
		http.Error(writer, "forbidden", http.StatusForbidden)
		return uuid.Nil, false
	}
	return studentID, true
}

func (handler *Handler) ParentIntervention(writer http.ResponseWriter, request *http.Request) {
	principal, _ := auth.PrincipalFromContext(request.Context())
	parentID, err := uuid.Parse(principal.UserID)
	if err != nil {
		http.Error(writer, "invalid principal", http.StatusUnauthorized)
		return
	}
	studentID, err := uuid.Parse(request.PathValue("student_id"))
	if err != nil {
		http.Error(writer, "invalid student id", http.StatusBadRequest)
		return
	}
	allowed, err := handler.parents.CanSupervise(request.Context(), parentID, studentID)
	if err != nil || !allowed {
		http.Error(writer, "forbidden", http.StatusForbidden)
		return
	}
	var body struct {
		Type string `json:"type"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		http.Error(writer, "invalid intervention", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		http.Error(writer, "invalid intervention", http.StatusBadRequest)
		return
	}
	messages := map[string]string{
		"ENCOURAGEMENT":    "家长在关注你的努力，继续按自己的思路来。",
		"REDUCE_INTENSITY": "今天会适当减轻强度，先把眼前这一步做好。",
		"REVIEW_ONLY":      "后续计划将优先复习已经学过的内容。",
		"STATE_NOT_GOOD":   "今天状态不佳时可以放慢节奏，系统会减少新内容。",
	}
	message, valid := messages[body.Type]
	if !valid {
		http.Error(writer, "unsupported intervention", http.StatusBadRequest)
		return
	}
	var event *realtime.Event
	err = pgx.BeginFunc(request.Context(), handler.pool, func(tx pgx.Tx) error {
		var lockedStudentID uuid.UUID
		if err := tx.QueryRow(request.Context(), `SELECT id FROM students WHERE id=$1 FOR NO KEY UPDATE`, studentID).Scan(&lockedStudentID); err != nil {
			return err
		}
		var sessionID *uuid.UUID
		if err := tx.QueryRow(request.Context(), `SELECT id FROM learning_sessions WHERE student_id=$1 AND status='ACTIVE' ORDER BY started_at DESC LIMIT 1 FOR UPDATE`, studentID).Scan(&sessionID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err := tx.Exec(request.Context(), `INSERT INTO parent_interventions(id,parent_user_id,student_id,session_id,type,payload_json)VALUES($1,$2,$3,$4,$5,'{}')`, uuid.New(), parentID, studentID, sessionID, body.Type); err != nil {
			return err
		}
		if body.Type == "REDUCE_INTENSITY" || body.Type == "STATE_NOT_GOOD" {
			if _, err := tx.Exec(request.Context(), `UPDATE learning_sessions SET target_minutes=LEAST(target_minutes,GREATEST(5,target_minutes*2/3)),engagement_state=CASE WHEN $2='STATE_NOT_GOOD' THEN 'LOW' ELSE engagement_state END,version=version+1 WHERE student_id=$1 AND status='ACTIVE'`, studentID, body.Type); err != nil {
				return err
			}
		}
		if body.Type == "REDUCE_INTENSITY" || body.Type == "REVIEW_ONLY" || body.Type == "STATE_NOT_GOOD" {
			_, err := tx.Exec(request.Context(), `
INSERT INTO parent_preferences(parent_user_id,student_id,daily_minutes,priority_subject_codes,review_only,reduce_intensity)
VALUES($1,$2,30,'{}',$3='REVIEW_ONLY',$3 IN('REDUCE_INTENSITY','STATE_NOT_GOOD'))
ON CONFLICT(parent_user_id,student_id) DO UPDATE SET
review_only=parent_preferences.review_only OR EXCLUDED.review_only,
reduce_intensity=parent_preferences.reduce_intensity OR EXCLUDED.reduce_intensity,
updated_at=now()`, parentID, studentID, body.Type)
			if err != nil {
				return err
			}
		}
		if sessionID != nil {
			_, sequence, err := nextSequences(request.Context(), tx, *sessionID)
			if err != nil {
				return err
			}
			studentPayload, _ := json.Marshal(map[string]any{"intervention": body.Type, "message": message})
			parentPayload, _ := json.Marshal(map[string]any{"intervention": body.Type, "message": message, "answer_controls_available": false})
			created := makeEvent(studentID, *sessionID, sequence, realtime.EventParentIntervention, studentPayload, parentPayload, handler.now())
			if err := insertEvent(request.Context(), tx, created); err != nil {
				return err
			}
			event = &created
		}
		return nil
	})
	if err != nil {
		http.Error(writer, "intervention unavailable", http.StatusInternalServerError)
		return
	}
	if event != nil && handler.service.hub != nil {
		_ = handler.service.hub.Publish(*event)
	}
	writeJSON(writer, http.StatusOK, map[string]any{"saved": true, "answer_controls_available": false})
}
