package integration

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/oppositenum/ai-learning-tutor/server/internal/contentpipeline"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

func TestEveryCuratedSeedAssetPassesDeterministicValidation(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	repository := contentpipeline.NewRepository(pool)
	rows, err := pool.Query(ctx, `SELECT id FROM questions WHERE id::text LIKE '40000000-0000-4000-8000-0000000000%' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	var questionIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		questionIDs = append(questionIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(questionIDs) != 17 {
		t.Fatalf("curated seed count=%d want=17", len(questionIDs))
	}
	for _, questionID := range questionIDs {
		asset, generation, err := repository.LoadAsset(ctx, questionID)
		if err != nil {
			t.Fatalf("load seed %s: %v", questionID, err)
		}
		if generation.Provider != "internal" || generation.Model != "curated-v1" {
			t.Errorf("seed %s provenance=%s/%s", questionID, generation.Provider, generation.Model)
		}
		validation := (contentpipeline.Validator{}).Validate(asset)
		if !validation.Passed {
			t.Errorf("seed %s failed deterministic validation: %+v", questionID, validation.Checks)
		}
	}
}

func TestRemoveParenthesesLifeQuestionPassesDeterministicValidation(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	questionID := uuid.MustParse("40000000-0000-4000-8000-000000000017")
	asset, generation, err := contentpipeline.NewRepository(pool).LoadAsset(ctx, questionID)
	if err != nil {
		t.Fatalf("load 000036 seed: %v", err)
	}
	if generation.Provider != "internal" || generation.Model != "curated-v1" {
		t.Fatalf("000036 provenance=%s/%s", generation.Provider, generation.Model)
	}
	validation := (contentpipeline.Validator{}).Validate(asset)
	if !validation.Passed {
		t.Fatalf("000036 failed deterministic validation: %+v", validation.Checks)
	}
}
