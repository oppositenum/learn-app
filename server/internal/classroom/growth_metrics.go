package classroom

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const growthEvidencePolicyVersion = "growth-evidence-v1"
const growthEvidenceEventLimit = 20

type growthEvidenceEvent struct {
	SourceKind     string    `json:"source_kind"`
	SourceID       uuid.UUID `json:"source_id"`
	Subject        string    `json:"subject"`
	KnowledgePoint string    `json:"knowledge_point"`
	Stage          *string   `json:"stage,omitempty"`
	EvidenceForm   *string   `json:"evidence_form,omitempty"`
	OccurredAt     time.Time `json:"occurred_at"`
}

type growthIndicator struct {
	Code            string                `json:"code"`
	Label           string                `json:"label"`
	Count           int                   `json:"count"`
	Events          []growthEvidenceEvent `json:"events"`
	EventsTruncated bool                  `json:"events_truncated"`
}

type growthProjection struct {
	PolicyVersion string            `json:"policy_version"`
	Indicators    []growthIndicator `json:"indicators"`
}

var growthIndicatorLabels = []struct {
	code  string
	label string
}{
	{code: "INDEPENDENT_SOLVING", label: "独立解决"},
	{code: "UNDERSTANDING_AFTER_HELP", label: "帮助后理解"},
	{code: "SELF_CORRECTION", label: "自我纠正"},
	{code: "TRANSFER_SUCCESS", label: "迁移成功"},
	{code: "DELAYED_REVIEW", label: "延迟复习"},
}

func loadGrowthProjection(ctx context.Context, pool *pgxpool.Pool, studentID uuid.UUID) (growthProjection, error) {
	projection := growthProjection{PolicyVersion: growthEvidencePolicyVersion}
	byCode := make(map[string]int, len(growthIndicatorLabels))
	for _, definition := range growthIndicatorLabels {
		projection.Indicators = append(projection.Indicators, growthIndicator{
			Code: definition.code, Label: definition.label, Events: []growthEvidenceEvent{},
		})
		byCode[definition.code] = len(projection.Indicators) - 1
	}

	rows, err := pool.Query(ctx, `
WITH growth_events AS (
    SELECT 'INDEPENDENT_SOLVING'::text AS metric_code,
           'CLASSROOM_STAGE_EVIDENCE'::text AS source_kind, evidence.id AS source_id,
           subject.code AS subject, knowledge_point.name AS knowledge_point,
           evidence.stage_role AS stage, evidence.evidence_form, evidence.created_at AS occurred_at
    FROM classroom_stage_evidence evidence
    JOIN knowledge_points knowledge_point ON knowledge_point.id=evidence.knowledge_point_id
    JOIN subjects subject ON subject.id=knowledge_point.subject_id
    WHERE evidence.student_id=$1 AND evidence.evidence_kind='INDEPENDENT'
    UNION ALL
    SELECT 'UNDERSTANDING_AFTER_HELP', 'CLASSROOM_STAGE_EVIDENCE', evidence.id,
           subject.code, knowledge_point.name, evidence.stage_role, evidence.evidence_form, evidence.created_at
    FROM classroom_stage_evidence evidence
    JOIN knowledge_points knowledge_point ON knowledge_point.id=evidence.knowledge_point_id
    JOIN subjects subject ON subject.id=knowledge_point.subject_id
    WHERE evidence.student_id=$1 AND evidence.evidence_kind='ASSISTED'
    UNION ALL
    SELECT 'INDEPENDENT_SOLVING', 'MASTERY_EVIDENCE_PROVENANCE', evidence.id,
           subject.code, knowledge_point.name, NULL, evidence.evidence_form, evidence.created_at
    FROM mastery_evidence_provenance evidence
    JOIN knowledge_points knowledge_point ON knowledge_point.id=evidence.knowledge_point_id
    JOIN subjects subject ON subject.id=knowledge_point.subject_id
    WHERE evidence.student_id=$1 AND evidence.authorization_source='DETERMINISTIC_RULE'
    UNION ALL
    SELECT 'UNDERSTANDING_AFTER_HELP', 'ANSWER_EVALUATION_PROVENANCE', provenance.id,
           subject.code, knowledge_point.name, NULL, session.evidence_form, provenance.created_at
    FROM answer_evaluation_provenance provenance
    JOIN student_answers answer ON answer.id=provenance.student_answer_id
    JOIN answer_analyses analysis ON analysis.student_answer_id=answer.id
    JOIN learning_effect_events effect
      ON effect.source_kind='ANSWER_ANALYSIS' AND effect.source_id=analysis.id
    JOIN learning_sessions session ON session.id=answer.session_id
    JOIN questions question ON question.id=answer.question_id
    JOIN knowledge_points knowledge_point ON knowledge_point.id=question.knowledge_point_id
    JOIN subjects subject ON subject.id=knowledge_point.subject_id
    WHERE session.student_id=$1 AND provenance.deterministic_result='MATCH'
      AND effect.assistance_level>0
    UNION ALL
    SELECT 'SELF_CORRECTION', 'CLASSROOM_STAGE_ATTEMPT', attempt.id,
           subject.code, knowledge_point.name, attempt.submitted_stage, task.evidence_form, attempt.created_at
    FROM classroom_stage_attempts attempt
    JOIN classroom_stage_sessions stage_session ON stage_session.session_id=attempt.session_id
    JOIN classroom_stage_tasks task ON task.question_id=attempt.question_id
    JOIN knowledge_points knowledge_point ON knowledge_point.id=stage_session.knowledge_point_id
    JOIN subjects subject ON subject.id=knowledge_point.subject_id
    JOIN learning_sessions session ON session.id=attempt.session_id
    WHERE session.student_id=$1 AND attempt.deterministic_result='CORRECT'
      AND EXISTS (
          SELECT 1 FROM classroom_stage_attempts previous
          WHERE previous.session_id=attempt.session_id
            AND previous.submitted_stage=attempt.submitted_stage
            AND previous.deterministic_result IN ('INCORRECT','INDETERMINATE')
            AND (previous.created_at,previous.id)<(attempt.created_at,attempt.id)
      )
    UNION ALL
    SELECT 'SELF_CORRECTION', 'ANSWER_EVALUATION_PROVENANCE', provenance.id,
           subject.code, knowledge_point.name, NULL, session.evidence_form, provenance.created_at
    FROM answer_evaluation_provenance provenance
    JOIN student_answers answer ON answer.id=provenance.student_answer_id
    JOIN learning_sessions session ON session.id=answer.session_id
    JOIN questions question ON question.id=answer.question_id
    JOIN knowledge_points knowledge_point ON knowledge_point.id=question.knowledge_point_id
    JOIN subjects subject ON subject.id=knowledge_point.subject_id
    WHERE session.student_id=$1 AND provenance.deterministic_result='MATCH'
      AND EXISTS (
          SELECT 1 FROM answer_evaluation_provenance previous_provenance
          JOIN student_answers previous_answer ON previous_answer.id=previous_provenance.student_answer_id
          WHERE previous_answer.session_id=answer.session_id
            AND previous_answer.question_id=answer.question_id
            AND previous_provenance.final_correct=false
            AND (previous_answer.submitted_at,previous_answer.id)<(answer.submitted_at,answer.id)
      )
    UNION ALL
    SELECT 'TRANSFER_SUCCESS', 'CLASSROOM_STAGE_EVIDENCE', evidence.id,
           subject.code, knowledge_point.name, evidence.stage_role, evidence.evidence_form, evidence.created_at
    FROM classroom_stage_evidence evidence
    JOIN knowledge_points knowledge_point ON knowledge_point.id=evidence.knowledge_point_id
    JOIN subjects subject ON subject.id=knowledge_point.subject_id
    WHERE evidence.student_id=$1 AND evidence.evidence_kind='INDEPENDENT'
      AND evidence.stage_role='VARIANT'
    UNION ALL
    SELECT 'TRANSFER_SUCCESS', 'MASTERY_EVIDENCE_PROVENANCE', evidence.id,
           subject.code, knowledge_point.name, NULL, evidence.evidence_form, evidence.created_at
    FROM mastery_evidence_provenance evidence
    JOIN knowledge_points knowledge_point ON knowledge_point.id=evidence.knowledge_point_id
    JOIN subjects subject ON subject.id=knowledge_point.subject_id
    WHERE evidence.student_id=$1 AND evidence.authorization_source='DETERMINISTIC_RULE'
      AND evidence.evidence_form='VARIANT'
    UNION ALL
    SELECT 'DELAYED_REVIEW', 'REVIEW_QUEUE', review.id,
           subject.code, knowledge_point.name, NULL, 'REVIEW', review.resolved_at
    FROM review_queue review
    JOIN knowledge_points knowledge_point ON knowledge_point.id=review.knowledge_point_id
    JOIN subjects subject ON subject.id=knowledge_point.subject_id
    WHERE review.student_id=$1 AND review.status='COMPLETED'
      AND review.result='INDEPENDENT_SUCCESS' AND review.resolved_at IS NOT NULL
), ranked AS (
    SELECT growth_events.*,
           count(*) OVER(PARTITION BY metric_code)::int AS total_count,
           row_number() OVER(PARTITION BY metric_code ORDER BY occurred_at DESC,source_id) AS event_rank
    FROM growth_events
)
SELECT metric_code,source_kind,source_id,subject,knowledge_point,stage,evidence_form,
       occurred_at,total_count
FROM ranked
WHERE event_rank<=$2
ORDER BY metric_code,occurred_at DESC,source_id`, studentID, growthEvidenceEventLimit)
	if err != nil {
		return projection, err
	}
	defer rows.Close()
	for rows.Next() {
		var code string
		var event growthEvidenceEvent
		var total int
		if err := rows.Scan(&code, &event.SourceKind, &event.SourceID, &event.Subject,
			&event.KnowledgePoint, &event.Stage, &event.EvidenceForm, &event.OccurredAt, &total); err != nil {
			return projection, err
		}
		index, ok := byCode[code]
		if !ok {
			continue
		}
		indicator := &projection.Indicators[index]
		indicator.Count = total
		indicator.EventsTruncated = total > growthEvidenceEventLimit
		indicator.Events = append(indicator.Events, event)
	}
	return projection, rows.Err()
}

func recordLegacySupportLearningEffect(ctx context.Context, tx pgx.Tx, row sessionRow, sessionID, sourceID uuid.UUID, support SupportType, state string, assistance int, now time.Time) error {
	_, err := tx.Exec(ctx, `
INSERT INTO learning_effect_events(
    id,student_id,session_id,subject_id,knowledge_point_id,question_id,
    event_type,source_kind,source_id,outcome,support_type,classroom_state,
    evidence_form,assistance_level,review_interval_days,occurred_at
)
VALUES($1,$2,$3,$4,$5,$6,'SUPPORT_REQUESTED','TUTOR_TURN',$7,'HELP_REQUESTED',
       $8,$9,$10,$11,learning_effect_review_interval($12),$13)
ON CONFLICT(event_type,source_kind,source_id) DO NOTHING`, uuid.New(), row.studentID,
		sessionID, row.subjectID, row.knowledgePointID, row.questionID, sourceID,
		string(support), state, row.evidenceForm, assistance, row.reviewQueueID, now)
	return err
}
