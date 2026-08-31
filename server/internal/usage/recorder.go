package usage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
)

var ErrPriceNotFound = errors.New("no effective AI price catalog entry")

type Recorder struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewRecorder(pool *pgxpool.Pool) *Recorder { return &Recorder{pool: pool, now: time.Now} }

func (recorder *Recorder) EnsurePrice(ctx context.Context, provider, model string, at time.Time) error {
	if recorder.pool == nil {
		return errors.New("usage database is required")
	}
	if at.IsZero() {
		at = recorder.now()
	}
	var exists bool
	err := recorder.pool.QueryRow(ctx, `SELECT EXISTS(
SELECT 1 FROM ai_price_catalog
WHERE provider=$1 AND model=$2 AND effective_from<=$3
  AND (effective_to IS NULL OR effective_to>$3)
)`, provider, model, at).Scan(&exists)
	if err != nil {
		return fmt.Errorf("check AI price catalog: %w", err)
	}
	if !exists {
		return fmt.Errorf("%w: %s/%s at %s", ErrPriceNotFound, provider, model, at.Format(time.RFC3339))
	}
	return nil
}

func (recorder *Recorder) RecordAIUsage(ctx context.Context, record ai.UsageRecord) error {
	if recorder.pool == nil {
		return errors.New("usage database is required")
	}
	at := record.CreatedAt
	if at.IsZero() {
		at = recorder.now()
	}
	var catalogID uuid.UUID
	var price Price
	err := recorder.pool.QueryRow(ctx, `
SELECT id, input_price_per_million_usd::text, cached_input_price_per_million_usd::text,
       output_price_per_million_usd::text, audio_input_price_per_minute_usd::text,
       audio_output_price_per_minute_usd::text
FROM ai_price_catalog
WHERE provider = $1 AND model = $2 AND effective_from <= $3
  AND (effective_to IS NULL OR effective_to > $3)
ORDER BY effective_from DESC LIMIT 1`, record.Usage.Provider, record.Usage.Model, at).Scan(
		&catalogID, &price.InputPerMillion, &price.CachedInputPerMillion, &price.OutputPerMillion,
		&price.AudioInputPerMinute, &price.AudioOutputPerMinute,
	)
	if err != nil {
		return fmt.Errorf("%w: %s/%s at %s: %v", ErrPriceNotFound, record.Usage.Provider, record.Usage.Model, at.Format(time.RFC3339), err)
	}
	cost, err := Calculate(price, Amounts{InputTokens: record.Usage.InputTokens, CachedInputTokens: record.Usage.CachedInputTokens, OutputTokens: record.Usage.OutputTokens, AudioInputSeconds: record.Usage.AudioInputSeconds, AudioOutputSeconds: record.Usage.AudioOutputSeconds})
	if err != nil {
		return fmt.Errorf("calculate AI usage: %w", err)
	}
	requestID := record.RequestID
	if requestID == "" {
		return errors.New("usage request ID is required")
	}
	_, err = recorder.pool.Exec(ctx, `
INSERT INTO ai_usage_records
    (request_id, student_id, session_id, provider, model, purpose, input_tokens,
	 cached_input_tokens, output_tokens, audio_input_seconds, audio_output_seconds,
	 estimated_cost_usd, price_catalog_id, latency_ms, created_at)
VALUES ($1, NULLIF($2, '')::uuid, NULLIF($3, '')::uuid, $4, $5, $6, $7, $8, $9,
	$10::numeric, $11::numeric, $12::numeric, $13, $14, $15)
ON CONFLICT (request_id) DO NOTHING`, requestID, record.StudentID, record.SessionID,
		record.Usage.Provider, record.Usage.Model, string(record.Purpose), record.Usage.InputTokens,
		record.Usage.CachedInputTokens, record.Usage.OutputTokens, decimalValue(record.Usage.AudioInputSeconds),
		decimalValue(record.Usage.AudioOutputSeconds), cost.FloatString(9), catalogID,
		record.Latency.Milliseconds(), at)
	if err != nil {
		return fmt.Errorf("insert AI usage: %w", err)
	}
	return nil
}

func decimalValue(value string) string {
	if value == "" {
		return "0"
	}
	return value
}
