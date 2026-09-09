package classroom

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
)

const (
	deterministicPolicyVersion = "normalized-string-equality-v1"
	semanticPolicyVersion      = "teaching-agent-answer-analysis-v1"
	legacyBehaviorVersion      = "legacy-model-override-v1"
)

type answerEvaluationProvenance struct {
	deterministicResult string
	modelAnswerCorrect  *bool
	modelConfidence     *float64
	semanticVersion     *string
	legacyResolution    string
	finalCorrect        bool
}

func legacyAnswerEvaluationProvenance(deterministicCorrect bool, analysis *ai.AnalyzeAnswerResult, finalCorrect bool) answerEvaluationProvenance {
	provenance := answerEvaluationProvenance{
		deterministicResult: "NO_MATCH",
		legacyResolution:    "NOT_ACCEPTED",
		finalCorrect:        finalCorrect,
	}
	if deterministicCorrect {
		provenance.deterministicResult = "MATCH"
		provenance.legacyResolution = "DETERMINISTIC_ACCEPTED"
	} else if finalCorrect {
		provenance.legacyResolution = "MODEL_MEDIATED_ACCEPTED"
	}
	if analysis != nil {
		answerCorrect := analysis.AnswerCorrect
		confidence := analysis.Confidence
		version := semanticPolicyVersion
		provenance.modelAnswerCorrect = &answerCorrect
		provenance.modelConfidence = &confidence
		provenance.semanticVersion = &version
	}
	return provenance
}

func insertAnswerEvaluationProvenance(ctx context.Context, tx pgx.Tx, id, studentAnswerID uuid.UUID, provenance answerEvaluationProvenance, now time.Time) error {
	_, err := tx.Exec(ctx, `
INSERT INTO answer_evaluation_provenance (
    id,student_answer_id,deterministic_result,deterministic_policy_version,
    model_answer_correct,model_confidence,semantic_policy_version,
    legacy_resolution,final_correct,behavior_policy_version,created_at
)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		id, studentAnswerID, provenance.deterministicResult, deterministicPolicyVersion,
		provenance.modelAnswerCorrect, provenance.modelConfidence, provenance.semanticVersion,
		provenance.legacyResolution, provenance.finalCorrect, legacyBehaviorVersion, now)
	return err
}

func insertMasteryEvidenceProvenance(ctx context.Context, tx pgx.Tx, studentAnswerID, evaluationID uuid.UUID, row sessionRow, provenance answerEvaluationProvenance, now time.Time) error {
	authorizationSource := "DETERMINISTIC_RULE"
	provenanceRisk := "NONE"
	if provenance.legacyResolution == "MODEL_MEDIATED_ACCEPTED" {
		authorizationSource = "LEGACY_MODEL_MEDIATED"
		provenanceRisk = "UNREVIEWED"
	}
	_, err := tx.Exec(ctx, `
INSERT INTO mastery_evidence_provenance (
    id,student_answer_id,answer_evaluation_provenance_id,student_id,
    knowledge_point_id,evidence_form,authorization_source,provenance_risk,created_at
)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, uuid.New(), studentAnswerID, evaluationID,
		row.studentID, row.knowledgePointID, row.evidenceForm, authorizationSource, provenanceRisk, now)
	return err
}
