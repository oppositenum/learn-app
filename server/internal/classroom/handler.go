package classroom

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
	"github.com/oppositenum/ai-learning-tutor/server/internal/parent"
	"github.com/oppositenum/ai-learning-tutor/server/internal/planner"
)

type Handler struct {
	service *Service
	pool    *pgxpool.Pool
	parents *parent.Repository
	planner *planner.Service
}

func NewHandler(service *Service, pool *pgxpool.Pool, parents *parent.Repository, planners ...*planner.Service) *Handler {
	handler := &Handler{service: service, pool: pool, parents: parents}
	if len(planners) > 0 {
		handler.planner = planners[0]
	}
	return handler
}

func (handler *Handler) SubmitAnswer(writer http.ResponseWriter, request *http.Request) {
	principal, ok := auth.PrincipalFromContext(request.Context())
	if !ok {
		http.Error(writer, "authentication required", http.StatusUnauthorized)
		return
	}
	userID, err := uuid.Parse(principal.UserID)
	if err != nil {
		http.Error(writer, "invalid principal", http.StatusUnauthorized)
		return
	}
	sessionID, err := uuid.Parse(request.PathValue("session_id"))
	if err != nil {
		http.Error(writer, "invalid session id", http.StatusBadRequest)
		return
	}
	var body struct {
		Answer string `json:"answer"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 64<<10)).Decode(&body); err != nil {
		http.Error(writer, "invalid answer payload", http.StatusBadRequest)
		return
	}
	result, err := handler.service.Submit(request.Context(), userID, sessionID, body.Answer)
	if errors.Is(err, ErrVoiceReturnRequired) {
		http.Error(writer, "return to the original question before answering", http.StatusConflict)
		return
	}
	if errors.Is(err, ErrSessionNotFound) {
		http.Error(writer, "session not found", http.StatusNotFound)
		return
	}
	if err != nil {
		log.Printf("classroom submit failed: %v", err)
		http.Error(writer, "answer could not be processed", http.StatusInternalServerError)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (handler *Handler) ReturnFromVoice(writer http.ResponseWriter, request *http.Request) {
	principal, _ := auth.PrincipalFromContext(request.Context())
	userID, err := uuid.Parse(principal.UserID)
	if err != nil {
		http.Error(writer, "invalid principal", http.StatusUnauthorized)
		return
	}
	sessionID, err := uuid.Parse(request.PathValue("session_id"))
	if err != nil {
		http.Error(writer, "invalid session id", http.StatusBadRequest)
		return
	}
	result, err := handler.service.ReturnFromVoice(request.Context(), userID, sessionID)
	if errors.Is(err, ErrSessionNotFound) {
		http.Error(writer, "session not found", http.StatusNotFound)
		return
	}
	if errors.Is(err, ErrVoiceNotActive) {
		http.Error(writer, "voice explanation is not active", http.StatusConflict)
		return
	}
	if err != nil {
		log.Printf("voice return failed: %v", err)
		http.Error(writer, "voice return could not be completed", http.StatusInternalServerError)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (handler *Handler) RequestSupport(writer http.ResponseWriter, request *http.Request) {
	principal, _ := auth.PrincipalFromContext(request.Context())
	userID, err := uuid.Parse(principal.UserID)
	if err != nil {
		http.Error(writer, "invalid principal", http.StatusUnauthorized)
		return
	}
	sessionID, err := uuid.Parse(request.PathValue("session_id"))
	if err != nil {
		http.Error(writer, "invalid session id", http.StatusBadRequest)
		return
	}
	var body struct {
		Type SupportType `json:"type"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		http.Error(writer, "invalid support request", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		http.Error(writer, "invalid support request", http.StatusBadRequest)
		return
	}
	result, err := handler.service.RequestSupport(request.Context(), userID, sessionID, body.Type)
	if errors.Is(err, ErrInvalidSupport) {
		http.Error(writer, "invalid support request", http.StatusBadRequest)
		return
	}
	if errors.Is(err, ErrSessionNotFound) {
		http.Error(writer, "session not found", http.StatusNotFound)
		return
	}
	if errors.Is(err, ErrClassroomChanged) {
		http.Error(writer, "classroom changed; retry support request", http.StatusConflict)
		return
	}
	if err != nil {
		log.Printf("classroom support failed: %v", err)
		http.Error(writer, "support could not be generated", http.StatusInternalServerError)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (handler *Handler) Growth(writer http.ResponseWriter, request *http.Request) {
	principal, _ := auth.PrincipalFromContext(request.Context())
	userID, err := uuid.Parse(principal.UserID)
	if err != nil {
		http.Error(writer, "invalid principal", http.StatusUnauthorized)
		return
	}
	var studentID uuid.UUID
	var energy, streak int
	var buildings json.RawMessage
	err = handler.pool.QueryRow(request.Context(), `SELECT st.id,COALESCE(g.total_energy,0),COALESCE(g.streak_days,0),COALESCE(g.buildings_json,'{}') FROM students st LEFT JOIN student_growth g ON g.student_id=st.id WHERE st.user_id=$1`, userID).Scan(&studentID, &energy, &streak, &buildings)
	if err != nil {
		http.Error(writer, "growth unavailable", http.StatusNotFound)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"student_id": studentID, "total_energy": energy, "streak_days": streak, "buildings": buildings})
}

func (handler *Handler) Today(writer http.ResponseWriter, request *http.Request) {
	principal, _ := auth.PrincipalFromContext(request.Context())
	userID, err := uuid.Parse(principal.UserID)
	if err != nil {
		http.Error(writer, "invalid principal", http.StatusUnauthorized)
		return
	}
	if handler.planner != nil {
		plan, err := handler.planner.EnsureForUser(request.Context(), userID, time.Now())
		if err != nil {
			http.Error(writer, "plan unavailable", 500)
			return
		}
		blocks := make([]map[string]any, 0, len(plan.Blocks))
		for _, block := range plan.Blocks {
			blocks = append(blocks, map[string]any{"id": block.ID, "sequence": block.Sequence, "subject": block.SubjectCode, "knowledge_point_id": block.KnowledgePointID, "minutes": block.Minutes, "mode": block.Mode, "reason": block.Reason, "focus": block.Focus, "original_task_id": block.OriginalTaskID})
		}
		writeJSON(writer, http.StatusOK, map[string]any{"plans": []map[string]any{{"id": plan.ID, "date": plan.Date, "target_minutes": plan.TargetMinutes, "blocks": blocks}}})
		return
	}
	rows, err := handler.pool.Query(request.Context(), `SELECT p.id,p.plan_date,p.target_minutes,b.sequence,s.code,b.minutes,b.mode,b.reason,kp.name FROM learning_plans p JOIN students st ON st.id=p.student_id LEFT JOIN learning_plan_blocks b ON b.plan_id=p.id LEFT JOIN subjects s ON s.id=b.subject_id LEFT JOIN knowledge_points kp ON kp.id=b.knowledge_point_id WHERE st.user_id=$1 AND p.status IN('PROPOSED','ACTIVE') ORDER BY p.plan_date,b.sequence`, userID)
	if err != nil {
		http.Error(writer, "plan unavailable", 500)
		return
	}
	defer rows.Close()
	type block struct {
		Sequence int16   `json:"sequence"`
		Subject  string  `json:"subject"`
		Minutes  int16   `json:"minutes"`
		Mode     string  `json:"mode"`
		Reason   string  `json:"reason"`
		Focus    *string `json:"focus,omitempty"`
	}
	type plan struct {
		ID     uuid.UUID `json:"id"`
		Date   time.Time `json:"date"`
		Target int16     `json:"target_minutes"`
		Blocks []block   `json:"blocks"`
	}
	plans := []plan{}
	indexes := map[uuid.UUID]int{}
	for rows.Next() {
		var id uuid.UUID
		var date time.Time
		var target, sequence, minutes int16
		var subject, mode, reason string
		var focus *string
		if err := rows.Scan(&id, &date, &target, &sequence, &subject, &minutes, &mode, &reason, &focus); err != nil {
			http.Error(writer, "plan unavailable", 500)
			return
		}
		index, exists := indexes[id]
		if !exists {
			index = len(plans)
			indexes[id] = index
			plans = append(plans, plan{ID: id, Date: date, Target: target, Blocks: []block{}})
		}
		plans[index].Blocks = append(plans[index].Blocks, block{sequence, subject, minutes, mode, reason, focus})
	}
	writeJSON(writer, http.StatusOK, map[string]any{"plans": plans})
}

var allSubjectCodes = []string{"MATH", "CHINESE", "ENGLISH", "PHYSICS", "CHEMISTRY"}

var validSubjectCodes = map[string]bool{"MATH": true, "CHINESE": true, "ENGLISH": true, "PHYSICS": true, "CHEMISTRY": true}

func (handler *Handler) ParentPreferences(writer http.ResponseWriter, request *http.Request) {
	principal, _ := auth.PrincipalFromContext(request.Context())
	parentID, err := uuid.Parse(principal.UserID)
	if err != nil {
		http.Error(writer, "invalid principal", 401)
		return
	}
	studentID, err := uuid.Parse(request.PathValue("student_id"))
	if err != nil {
		http.Error(writer, "invalid student id", 400)
		return
	}
	allowed, err := handler.parents.CanSupervise(request.Context(), parentID, studentID)
	if err != nil || !allowed {
		http.Error(writer, "forbidden", 403)
		return
	}
	var body struct {
		DailyMinutes     int16     `json:"daily_minutes"`
		PrioritySubjects []string  `json:"priority_subject_codes"`
		ReviewOnly       bool      `json:"review_only"`
		ReduceIntensity  bool      `json:"reduce_intensity"`
		EnabledSubjects  *[]string `json:"enabled_subject_codes"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 32<<10)).Decode(&body); err != nil {
		http.Error(writer, "invalid preferences", 400)
		return
	}
	if body.DailyMinutes < 15 || body.DailyMinutes > 60 {
		http.Error(writer, "daily_minutes must be between 15 and 60", 400)
		return
	}
	enabledSubjects := []string{}
	if body.EnabledSubjects != nil {
		seen := map[string]bool{}
		for _, code := range *body.EnabledSubjects {
			code = strings.ToUpper(strings.TrimSpace(code))
			if code == "" || seen[code] {
				continue
			}
			if !validSubjectCodes[code] {
				http.Error(writer, "enabled_subject_codes contains an unknown subject code", 400)
				return
			}
			seen[code] = true
			enabledSubjects = append(enabledSubjects, code)
		}
		if len(enabledSubjects) == 0 {
			http.Error(writer, "enabled_subject_codes must include at least one subject", 400)
			return
		}
	}
	_, err = handler.pool.Exec(request.Context(), `INSERT INTO parent_preferences(parent_user_id,student_id,daily_minutes,priority_subject_codes,review_only,reduce_intensity,enabled_subject_codes) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(parent_user_id,student_id) DO UPDATE SET daily_minutes=EXCLUDED.daily_minutes,priority_subject_codes=EXCLUDED.priority_subject_codes,review_only=EXCLUDED.review_only,reduce_intensity=EXCLUDED.reduce_intensity,enabled_subject_codes=EXCLUDED.enabled_subject_codes,updated_at=now()`, parentID, studentID, body.DailyMinutes, body.PrioritySubjects, body.ReviewOnly, body.ReduceIntensity, enabledSubjects)
	if err != nil {
		http.Error(writer, "preferences unavailable", 500)
		return
	}
	replanned := false
	if handler.planner != nil {
		_, replanned, err = handler.planner.ReplaceToday(request.Context(), studentID, time.Now())
		if err != nil {
			http.Error(writer, "preferences saved but plan could not be updated", 500)
			return
		}
	}
	writeJSON(writer, http.StatusOK, map[string]any{"saved": true, "plan_replaced": replanned, "answer_controls_available": false})
}

func (handler *Handler) GetParentPreferences(writer http.ResponseWriter, request *http.Request) {
	principal, _ := auth.PrincipalFromContext(request.Context())
	parentID, err := uuid.Parse(principal.UserID)
	if err != nil {
		http.Error(writer, "invalid principal", 401)
		return
	}
	studentID, err := uuid.Parse(request.PathValue("student_id"))
	if err != nil {
		http.Error(writer, "invalid student id", 400)
		return
	}
	allowed, err := handler.parents.CanSupervise(request.Context(), parentID, studentID)
	if err != nil || !allowed {
		http.Error(writer, "forbidden", 403)
		return
	}
	dailyMinutes := int16(30)
	priorities := []string{}
	reviewOnly, reduceIntensity, configured := false, false, true
	enabledSubjects := []string{}
	err = handler.pool.QueryRow(request.Context(), `SELECT daily_minutes,priority_subject_codes,review_only,reduce_intensity,enabled_subject_codes FROM parent_preferences WHERE student_id=$1 ORDER BY updated_at DESC LIMIT 1`, studentID).Scan(&dailyMinutes, &priorities, &reviewOnly, &reduceIntensity, &enabledSubjects)
	if errors.Is(err, pgx.ErrNoRows) {
		configured = false
	} else if err != nil {
		http.Error(writer, "preferences unavailable", 500)
		return
	}
	if len(enabledSubjects) == 0 {
		enabledSubjects = allSubjectCodes
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"daily_minutes":          dailyMinutes,
		"priority_subject_codes": priorities,
		"review_only":            reviewOnly,
		"reduce_intensity":       reduceIntensity,
		"enabled_subject_codes":  enabledSubjects,
		"configured":             configured,
	})
}

func (handler *Handler) OwnerCosts(writer http.ResponseWriter, request *http.Request) {
	studentID, err := optionalUUID(request.URL.Query().Get("student_id"))
	if err != nil {
		http.Error(writer, "invalid student filter", 400)
		return
	}
	sessionID, err := optionalUUID(request.URL.Query().Get("session_id"))
	if err != nil {
		http.Error(writer, "invalid session filter", 400)
		return
	}
	dateFrom, err := optionalDate(request.URL.Query().Get("date_from"))
	if err != nil {
		http.Error(writer, "invalid date_from filter", 400)
		return
	}
	dateTo, err := optionalDate(request.URL.Query().Get("date_to"))
	if err != nil {
		http.Error(writer, "invalid date_to filter", 400)
		return
	}
	subject, model, purpose := request.URL.Query().Get("subject"), request.URL.Query().Get("model"), request.URL.Query().Get("purpose")
	filter := ` FROM ai_usage_records aur LEFT JOIN learning_sessions ls ON ls.id=aur.session_id LEFT JOIN subjects sub ON sub.id=ls.subject_id
WHERE ($1::uuid IS NULL OR aur.student_id=$1) AND ($2='' OR sub.code=$2)
AND ($3::date IS NULL OR aur.created_at::date >= $3) AND ($4::date IS NULL OR aur.created_at::date <= $4)
AND ($5='' OR aur.model=$5) AND ($6='' OR aur.purpose=$6) AND ($7::uuid IS NULL OR aur.session_id=$7)`
	arguments := []any{studentID, subject, dateFrom, dateTo, model, purpose, sessionID}
	rows, err := handler.pool.Query(request.Context(), `SELECT aur.created_at::date::text,aur.model,aur.purpose,count(*),sum(aur.input_tokens),sum(aur.cached_input_tokens),sum(aur.output_tokens),sum(aur.audio_input_seconds),sum(aur.audio_output_seconds),sum(aur.estimated_cost_usd)::text`+filter+` GROUP BY aur.created_at::date,aur.model,aur.purpose ORDER BY aur.created_at::date DESC,aur.model,aur.purpose`, arguments...)
	if err != nil {
		http.Error(writer, "cost report unavailable", 500)
		return
	}
	defer rows.Close()
	records := []map[string]any{}
	for rows.Next() {
		var date, model, purpose, cost string
		var requests, input, cached, output int64
		var audioIn, audioOut float64
		if err := rows.Scan(&date, &model, &purpose, &requests, &input, &cached, &output, &audioIn, &audioOut, &cost); err != nil {
			http.Error(writer, "cost report unavailable", 500)
			return
		}
		records = append(records, map[string]any{"date": date, "model": model, "purpose": purpose, "requests": requests, "input_tokens": input, "cached_input_tokens": cached, "output_tokens": output, "audio_input_seconds": audioIn, "audio_output_seconds": audioOut, "estimated_cost_usd": cost})
	}
	var totalCost, perStudentDay, per20Minutes, perMastered, cachedRatio, sttCost, ttsCost, strongRatio, averageTokens string
	summarySQL := `WITH filtered AS (SELECT aur.*` + filter + `),
session_minutes AS (SELECT COALESCE(sum(ls.target_minutes),0)::numeric AS value FROM learning_sessions ls WHERE ls.id IN(SELECT DISTINCT session_id FROM filtered WHERE session_id IS NOT NULL)),
mastered AS (SELECT count(*)::numeric AS value FROM student_skill_states ss WHERE ss.state='MASTERED' AND ss.student_id IN(SELECT DISTINCT student_id FROM filtered WHERE student_id IS NOT NULL)),
totals AS (SELECT COALESCE(sum(estimated_cost_usd),0)::numeric cost,count(*)::numeric requests,count(DISTINCT (student_id,created_at::date))::numeric student_days,COALESCE(sum(input_tokens),0)::numeric inputs,COALESCE(sum(cached_input_tokens),0)::numeric cached,COALESCE(sum(input_tokens+output_tokens),0)::numeric tokens,COALESCE(sum(estimated_cost_usd) FILTER(WHERE purpose='STT_TRANSCRIPTION'),0)::numeric stt,COALESCE(sum(estimated_cost_usd) FILTER(WHERE purpose='TTS_EXPLANATION'),0)::numeric tts,COALESCE(count(*) FILTER(WHERE price_catalog_id IN(SELECT id FROM ai_price_catalog WHERE cost_tier='STRONG')),0)::numeric strong FROM filtered)
SELECT cost::text,(CASE WHEN student_days=0 THEN 0 ELSE cost/student_days END)::text,(CASE WHEN (SELECT value FROM session_minutes)=0 THEN 0 ELSE cost*20/(SELECT value FROM session_minutes) END)::text,(CASE WHEN (SELECT value FROM mastered)=0 THEN 0 ELSE cost/(SELECT value FROM mastered) END)::text,(CASE WHEN inputs=0 THEN 0 ELSE cached/inputs END)::text,stt::text,tts::text,(CASE WHEN requests=0 THEN 0 ELSE strong/requests END)::text,(CASE WHEN requests=0 THEN 0 ELSE tokens/requests END)::text FROM totals`
	if err := handler.pool.QueryRow(request.Context(), summarySQL, arguments...).Scan(&totalCost, &perStudentDay, &per20Minutes, &perMastered, &cachedRatio, &sttCost, &ttsCost, &strongRatio, &averageTokens); err != nil {
		http.Error(writer, "cost summary unavailable", 500)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"records": records, "summary": map[string]any{"total_cost_usd": totalCost, "cost_per_active_student_day_usd": perStudentDay, "cost_per_20_minute_lesson_usd": per20Minutes, "cost_per_mastered_skill_usd": perMastered, "cached_ratio": cachedRatio, "stt_cost_usd": sttCost, "tts_cost_usd": ttsCost, "strong_model_ratio": strongRatio, "average_tokens_per_request": averageTokens}})
}

func optionalUUID(value string) (*uuid.UUID, error) {
	if value == "" {
		return nil, nil
	}
	id, err := uuid.Parse(value)
	return &id, err
}

func optionalDate(value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse("2006-01-02", value)
	return &parsed, err
}

func (handler *Handler) OwnerContent(writer http.ResponseWriter, request *http.Request) {
	rows, err := handler.pool.Query(request.Context(), `
SELECT q.id,q.status,q.content_version,s.code,kp.name,q.prompt_public,
       EXISTS(SELECT 1 FROM content_validations v WHERE v.question_id=q.id AND v.content_version=q.content_version AND v.status='PASS'),
       EXISTS(SELECT 1 FROM content_reviews r WHERE r.question_id=q.id AND r.content_version=q.content_version AND r.result='PASS')
FROM questions q JOIN knowledge_points kp ON kp.id=q.knowledge_point_id JOIN subjects s ON s.id=kp.subject_id
ORDER BY q.updated_at DESC,q.id LIMIT 200`)
	if err != nil {
		http.Error(writer, "content report unavailable", 500)
		return
	}
	defer rows.Close()
	records := []map[string]any{}
	for rows.Next() {
		var id uuid.UUID
		var status, version, subject, knowledge, prompt string
		var validated, reviewed bool
		if err := rows.Scan(&id, &status, &version, &subject, &knowledge, &prompt, &validated, &reviewed); err != nil {
			http.Error(writer, "content report unavailable", 500)
			return
		}
		records = append(records, map[string]any{"id": id, "status": status, "content_version": version, "subject": subject, "knowledge_point": knowledge, "prompt": prompt, "automatic_validation_passed": validated, "secondary_review_passed": reviewed})
	}
	writeJSON(writer, http.StatusOK, map[string]any{"records": records})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
