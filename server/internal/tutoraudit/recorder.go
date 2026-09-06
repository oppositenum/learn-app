package tutoraudit

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRecorder struct {
	pool *pgxpool.Pool
}

func NewPostgresRecorder(pool *pgxpool.Pool) *PostgresRecorder {
	return &PostgresRecorder{pool: pool}
}

func (recorder *PostgresRecorder) RecordTutorOutputAudit(ctx context.Context, record AuditRecord) error {
	if recorder == nil || recorder.pool == nil {
		return errors.New("Tutor output audit database is required")
	}
	_, err := recorder.pool.Exec(ctx, `
INSERT INTO tutor_output_audits
    (id,student_id,session_id,generation_response_id,reviewer_provider,reviewer_model,
     reviewer_request_id,policy_version,deterministic_result,reviewer_result,final_result,reason_code)
VALUES ($1,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		uuid.New(), record.StudentID, record.SessionID, record.GenerationResponseID,
		record.ReviewerProvider, record.ReviewerModel, record.ReviewerRequestID,
		record.PolicyVersion, record.DeterministicResult, record.ReviewerResult,
		record.FinalResult, record.ReasonCode)
	if err != nil {
		return fmt.Errorf("insert Tutor output audit: %w", err)
	}
	return nil
}
