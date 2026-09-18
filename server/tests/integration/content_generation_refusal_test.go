package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
	"github.com/oppositenum/ai-learning-tutor/server/internal/contentpipeline"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/internal/usage"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

// contentWriteTables are every table the content pipeline writes on a successful
// generation or release. A generation that exhausted its attempts must leave all
// of them exactly as it found them.
var contentWriteTables = []string{
	"questions",
	"question_private_answers",
	"content_versions",
	"content_validations",
	"content_reviews",
	"content_release_records",
}

// TestExhaustedContentGenerationLeavesNoPartialState proves the fail-closed half
// of the resample behaviour at the service layer rather than the generator layer:
// after both attempts return output this service refuses, no draft question, no
// content version, no provenance row and no release-state change may survive. The
// contentpipeline Repository wraps *pgxpool.Pool directly with no interface to
// substitute, so this runs against real PostgreSQL; a fake would not prove the
// absence of writes that only the real repository performs.
func TestExhaustedContentGenerationLeavesNoPartialState(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}

	const provider, model = "doubao", "doubao-seed-2-1-pro-260628"
	if _, err := pool.Exec(ctx, `INSERT INTO ai_price_catalog
(id,provider,model,effective_from,input_price_per_million_usd,cached_input_price_per_million_usd,output_price_per_million_usd)
VALUES ($1,$2,$3,now()-interval '1 hour',1,0.5,2)`, uuid.New(), provider, model); err != nil {
		t.Fatalf("seed price: %v", err)
	}

	// A raw newline inside a JSON string: the failure shape observed live. The
	// provider answers 2xx every time, so nothing upstream of the schema check
	// can tell this generation apart from a successful one.
	const refused = "{\"questions\":[{\"prompt_public\":\"第一行\n第二行\"}]}"
	served := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		served++
		// Encoding the refused body as a JSON string turns its raw newline into a
		// \n escape on the wire, so the reply itself parses and the newline only
		// reappears — unescaped and invalid — inside the model's own output.
		quoted, _ := json.Marshal(refused)
		_, _ = writer.Write([]byte(`{"id":"refused-response","model":"` + model +
			`","choices":[{"message":{"content":` + string(quoted) +
			`}}],"usage":{"prompt_tokens":40,"completion_tokens":12}}`))
	}))
	defer server.Close()

	client, err := ai.NewStructuredProviderClient(server.Client(), server.URL, "key",
		provider, model, ai.ShapeChatCompletions, nil)
	if err != nil {
		t.Fatal(err)
	}
	generator, err := contentpipeline.NewOpenAIGenerator(
		client.WithUsageRecorder(usage.NewRecorder(pool)), provider, model)
	if err != nil {
		t.Fatal(err)
	}
	service := contentpipeline.NewService(
		contentpipeline.NewRepository(pool), contentpipeline.Validator{}, nil).WithGenerator(generator)

	before := tableCounts(t, ctx, pool, contentWriteTables)

	assets, err := service.GenerateDrafts(ctx, contentpipeline.GenerateRequest{
		KnowledgePointID: "30000000-0000-4000-8000-000000000001",
		SourceID:         "50000000-0000-4000-8000-000000000001",
		Difficulty:       "L2",
		QuestionType:     "FREE_TEXT",
		Count:            1,
		Requirements:     "使用原创生活场景",
	})
	if err == nil {
		t.Fatal("two refused outputs produced drafts")
	}
	if !errors.Is(err, ai.ErrInvalidProviderOutput) {
		t.Fatalf("failure was not classified as refused provider output: %v", err)
	}
	if len(assets) != 0 {
		t.Fatalf("a failed generation returned %d asset(s)", len(assets))
	}
	if served != 2 {
		t.Fatalf("provider served %d replies, want 2 attempts", served)
	}

	after := tableCounts(t, ctx, pool, contentWriteTables)
	for _, table := range contentWriteTables {
		if after[table] != before[table] {
			t.Errorf("%s changed from %d to %d rows after a failed generation",
				table, before[table], after[table])
		}
	}

	// Both attempts still reached the provider and must be paid for, so the
	// ledger is the one place that is expected to grow. This also rules out the
	// test passing because generation never left the service.
	var accounted int
	if err := pool.QueryRow(ctx, `
SELECT count(*) FROM ai_usage_records
WHERE purpose='CONTENT_GENERATION' AND provider=$1 AND model=$2 AND price_catalog_id IS NOT NULL`,
		provider, model).Scan(&accounted); err != nil {
		t.Fatal(err)
	}
	if accounted != 2 {
		t.Fatalf("accounted content generation calls=%d, want 2 priced attempts", accounted)
	}
	var distinct int
	if err := pool.QueryRow(ctx,
		`SELECT count(DISTINCT request_id) FROM ai_usage_records WHERE purpose='CONTENT_GENERATION'`).
		Scan(&distinct); err != nil {
		t.Fatal(err)
	}
	if distinct != 2 {
		t.Fatalf("distinct request ids=%d, want 2: the attempts are not separable in the ledger", distinct)
	}
}

func tableCounts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tables []string) map[string]int {
	t.Helper()
	counts := make(map[string]int, len(tables))
	for _, table := range tables {
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		counts[table] = count
	}
	return counts
}
