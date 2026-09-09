package classroom

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type metricRate struct {
	Rate *float64 `json:"rate"`
}

type firstAnswerLatencyMetric struct {
	Sessions         int      `json:"sessions"`
	MeasuredSessions int      `json:"measured_sessions"`
	AverageMS        *float64 `json:"average_ms"`
}

type interactionShareMetric struct {
	metricRate
	StudentEvents int `json:"student_events"`
	TutorEvents   int `json:"tutor_events"`
}

type assistanceCompletionMetric struct {
	metricRate
	AssistanceLevel int `json:"assistance_level"`
	Completed       int `json:"completed"`
	Exits           int `json:"exits"`
}

type accuracyMetric struct {
	metricRate
	Correct  int `json:"correct"`
	Attempts int `json:"attempts"`
}

type retentionMetric struct {
	metricRate
	Days     int `json:"days"`
	Correct  int `json:"correct"`
	Attempts int `json:"attempts"`
}

type reengagementMetric struct {
	metricRate
	Requests  int `json:"requests"`
	Reengaged int `json:"reengaged"`
}

type exitStageMetric struct {
	Stage   string `json:"stage"`
	Outcome string `json:"outcome"`
	Count   int    `json:"count"`
}

type aiAnomalyMetric struct {
	metricRate
	Requests  int `json:"requests"`
	Anomalies int `json:"anomalies"`
}

type learningEffectReport struct {
	Definitions                 map[string]string            `json:"definitions"`
	FirstAnswerEffectiveLatency firstAnswerLatencyMetric     `json:"first_answer_effective_latency"`
	StudentInteractionShare     interactionShareMetric       `json:"student_interaction_share"`
	CompletionByAssistance      []assistanceCompletionMetric `json:"completion_by_assistance"`
	TransferAccuracy            accuracyMetric               `json:"transfer_accuracy"`
	Retention                   []retentionMetric            `json:"retention"`
	DontKnowReengagement        reengagementMetric           `json:"dont_know_reengagement"`
	ExitStages                  []exitStageMetric            `json:"exit_stages"`
	AIAnomaly                   aiAnomalyMetric              `json:"ai_anomaly"`
}

type learningEffectFilters struct {
	studentID *uuid.UUID
	subject   string
	dateFrom  *time.Time
	dateTo    *time.Time
}

const learningEffectEventFilter = `
WHERE ($1::uuid IS NULL OR event.student_id=$1)
  AND ($2='' OR subject.code=$2)
  AND ($3::date IS NULL OR event.occurred_at::date >= $3)
  AND ($4::date IS NULL OR event.occurred_at::date <= $4)`

func (handler *Handler) OwnerLearningEffects(writer http.ResponseWriter, request *http.Request) {
	studentID, err := optionalUUID(request.URL.Query().Get("student_id"))
	if err != nil {
		http.Error(writer, "invalid student filter", http.StatusBadRequest)
		return
	}
	dateFrom, err := optionalDate(request.URL.Query().Get("date_from"))
	if err != nil {
		http.Error(writer, "invalid date_from filter", http.StatusBadRequest)
		return
	}
	dateTo, err := optionalDate(request.URL.Query().Get("date_to"))
	if err != nil {
		http.Error(writer, "invalid date_to filter", http.StatusBadRequest)
		return
	}
	if dateFrom != nil && dateTo != nil && dateFrom.After(*dateTo) {
		http.Error(writer, "date_from is after date_to", http.StatusBadRequest)
		return
	}
	filters := learningEffectFilters{
		studentID: studentID, subject: request.URL.Query().Get("subject"),
		dateFrom: dateFrom, dateTo: dateTo,
	}
	report, err := loadLearningEffectReport(request.Context(), handler.pool, filters)
	if err != nil {
		http.Error(writer, "learning-effect report unavailable", http.StatusInternalServerError)
		return
	}
	writeJSON(writer, http.StatusOK, report)
}

type learningEffectQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func loadLearningEffectReport(ctx context.Context, queryer learningEffectQueryer, filters learningEffectFilters) (learningEffectReport, error) {
	report := learningEffectReport{
		Definitions: map[string]string{
			"first_answer_effective_latency": "每个课堂第一次任务作答前的服务端有效学习时长；无法重建的历史时长不按零计算",
			"student_interaction_share":      "学生作答与主动求助事件数，除以学生作答、主动求助与 Tutor 输出事件总数",
			"completion_by_assistance":       "各帮助等级完成退出数，除以该等级全部完成或放弃退出数",
			"transfer_accuracy":              "变式任务正确次数，除以变式任务作答次数",
			"retention":                      "到期间隔为 D+1 或 D+7 的复习任务正确次数，除以相应复习作答次数",
			"dont_know_reengagement":         "主动请求 EXPLAIN 后再次作答的请求数，除以 EXPLAIN 请求总数",
			"ai_anomaly":                     "结构化 AI 请求的非成功结果数，除以已记录结果总数",
		},
		CompletionByAssistance: []assistanceCompletionMetric{},
		Retention:              []retentionMetric{},
		ExitStages:             []exitStageMetric{},
	}
	arguments := []any{filters.studentID, filters.subject, filters.dateFrom, filters.dateTo}

	latencySQL := `WITH filtered AS (
    SELECT event.*,row_number() OVER(PARTITION BY event.session_id ORDER BY event.occurred_at,event.source_id) AS attempt_number
    FROM learning_effect_events event
    LEFT JOIN subjects subject ON subject.id=event.subject_id
    ` + learningEffectEventFilter + ` AND event.event_type='TASK_ATTEMPT'
)
SELECT count(*)::int,count(effective_elapsed_ms)::int,avg(effective_elapsed_ms)::float8
FROM filtered WHERE attempt_number=1`
	if err := queryer.QueryRow(ctx, latencySQL, arguments...).Scan(
		&report.FirstAnswerEffectiveLatency.Sessions,
		&report.FirstAnswerEffectiveLatency.MeasuredSessions,
		&report.FirstAnswerEffectiveLatency.AverageMS,
	); err != nil {
		return report, err
	}

	interactionSQL := `SELECT
    count(*) FILTER(WHERE event.event_type IN('TASK_ATTEMPT','SUPPORT_REQUESTED'))::int,
    count(*) FILTER(WHERE event.event_type='TUTOR_OUTPUT')::int
FROM learning_effect_events event
LEFT JOIN subjects subject ON subject.id=event.subject_id
` + learningEffectEventFilter
	if err := queryer.QueryRow(ctx, interactionSQL, arguments...).Scan(
		&report.StudentInteractionShare.StudentEvents, &report.StudentInteractionShare.TutorEvents,
	); err != nil {
		return report, err
	}
	report.StudentInteractionShare.Rate = ratio(
		report.StudentInteractionShare.StudentEvents,
		report.StudentInteractionShare.StudentEvents+report.StudentInteractionShare.TutorEvents,
	)

	completionSQL := `WITH levels AS (SELECT generate_series(0,4)::int AS assistance_level), aggregated AS (
    SELECT event.assistance_level,
           count(*) FILTER(WHERE event.outcome='COMPLETED')::int AS completed,
           count(*)::int AS exits
    FROM learning_effect_events event
    LEFT JOIN subjects subject ON subject.id=event.subject_id
    ` + learningEffectEventFilter + ` AND event.event_type='SESSION_EXIT'
    GROUP BY event.assistance_level
)
SELECT levels.assistance_level,COALESCE(aggregated.completed,0),COALESCE(aggregated.exits,0)
FROM levels LEFT JOIN aggregated USING(assistance_level) ORDER BY levels.assistance_level`
	rows, err := queryer.Query(ctx, completionSQL, arguments...)
	if err != nil {
		return report, err
	}
	for rows.Next() {
		var metric assistanceCompletionMetric
		if err := rows.Scan(&metric.AssistanceLevel, &metric.Completed, &metric.Exits); err != nil {
			rows.Close()
			return report, err
		}
		metric.Rate = ratio(metric.Completed, metric.Exits)
		report.CompletionByAssistance = append(report.CompletionByAssistance, metric)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return report, err
	}
	rows.Close()

	transferSQL := `SELECT
    count(*) FILTER(WHERE event.outcome='CORRECT')::int,count(*)::int
FROM learning_effect_events event
LEFT JOIN subjects subject ON subject.id=event.subject_id
` + learningEffectEventFilter + ` AND event.event_type='TASK_ATTEMPT'
  AND (event.classroom_state='VARIANT' OR event.evidence_form='VARIANT')`
	if err := queryer.QueryRow(ctx, transferSQL, arguments...).Scan(
		&report.TransferAccuracy.Correct, &report.TransferAccuracy.Attempts,
	); err != nil {
		return report, err
	}
	report.TransferAccuracy.Rate = ratio(report.TransferAccuracy.Correct, report.TransferAccuracy.Attempts)

	retentionSQL := `WITH days(days) AS (VALUES (1),(7)), aggregated AS (
    SELECT event.review_interval_days AS days,
           count(*) FILTER(WHERE event.outcome='CORRECT')::int AS correct,count(*)::int AS attempts
    FROM learning_effect_events event
    LEFT JOIN subjects subject ON subject.id=event.subject_id
    ` + learningEffectEventFilter + ` AND event.event_type='TASK_ATTEMPT'
      AND event.review_interval_days IN(1,7)
    GROUP BY event.review_interval_days
)
SELECT days.days,COALESCE(aggregated.correct,0),COALESCE(aggregated.attempts,0)
FROM days LEFT JOIN aggregated USING(days) ORDER BY days.days`
	rows, err = queryer.Query(ctx, retentionSQL, arguments...)
	if err != nil {
		return report, err
	}
	for rows.Next() {
		var metric retentionMetric
		if err := rows.Scan(&metric.Days, &metric.Correct, &metric.Attempts); err != nil {
			rows.Close()
			return report, err
		}
		metric.Rate = ratio(metric.Correct, metric.Attempts)
		report.Retention = append(report.Retention, metric)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return report, err
	}
	rows.Close()

	reengagementSQL := `WITH requests AS (
    SELECT event.* FROM learning_effect_events event
    LEFT JOIN subjects subject ON subject.id=event.subject_id
    ` + learningEffectEventFilter + ` AND event.event_type='SUPPORT_REQUESTED' AND event.support_type='EXPLAIN'
)
SELECT count(*)::int,count(*) FILTER(WHERE EXISTS(
    SELECT 1 FROM learning_effect_events attempt
    WHERE attempt.session_id=requests.session_id AND attempt.event_type='TASK_ATTEMPT'
      AND (attempt.occurred_at,attempt.source_id)>(requests.occurred_at,requests.source_id)
))::int FROM requests`
	if err := queryer.QueryRow(ctx, reengagementSQL, arguments...).Scan(
		&report.DontKnowReengagement.Requests, &report.DontKnowReengagement.Reengaged,
	); err != nil {
		return report, err
	}
	report.DontKnowReengagement.Rate = ratio(report.DontKnowReengagement.Reengaged, report.DontKnowReengagement.Requests)

	exitSQL := `SELECT COALESCE(event.classroom_state,'UNKNOWN'),event.outcome,count(*)::int
FROM learning_effect_events event
LEFT JOIN subjects subject ON subject.id=event.subject_id
` + learningEffectEventFilter + ` AND event.event_type='SESSION_EXIT'
GROUP BY event.classroom_state,event.outcome ORDER BY event.classroom_state,event.outcome`
	rows, err = queryer.Query(ctx, exitSQL, arguments...)
	if err != nil {
		return report, err
	}
	for rows.Next() {
		var metric exitStageMetric
		if err := rows.Scan(&metric.Stage, &metric.Outcome, &metric.Count); err != nil {
			rows.Close()
			return report, err
		}
		report.ExitStages = append(report.ExitStages, metric)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return report, err
	}
	rows.Close()

	aiSQL := `SELECT count(*)::int,count(*) FILTER(WHERE outcome<>'SUCCEEDED')::int
FROM ai_request_outcomes outcome
LEFT JOIN learning_sessions session ON session.id=outcome.session_id
LEFT JOIN subjects subject ON subject.id=session.subject_id
WHERE ($1::uuid IS NULL OR COALESCE(outcome.student_id,session.student_id)=$1)
  AND ($2='' OR subject.code=$2)
  AND ($3::date IS NULL OR outcome.occurred_at::date >= $3)
  AND ($4::date IS NULL OR outcome.occurred_at::date <= $4)`
	if err := queryer.QueryRow(ctx, aiSQL, arguments...).Scan(
		&report.AIAnomaly.Requests, &report.AIAnomaly.Anomalies,
	); err != nil {
		return report, err
	}
	report.AIAnomaly.Rate = ratio(report.AIAnomaly.Anomalies, report.AIAnomaly.Requests)
	return report, nil
}

func ratio(numerator, denominator int) *float64 {
	if denominator == 0 {
		return nil
	}
	value := float64(numerator) / float64(denominator)
	return &value
}
