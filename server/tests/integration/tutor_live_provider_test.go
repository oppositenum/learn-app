//go:build liveprovider

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
	"github.com/oppositenum/ai-learning-tutor/server/internal/content"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/internal/tutor"
	"github.com/oppositenum/ai-learning-tutor/server/internal/tutoraudit"
	"github.com/oppositenum/ai-learning-tutor/server/internal/usage"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

// TestLiveTutorProviderEndToEnd drives the production composition — structured
// client, CodexProvider, deterministic gate, independent reviewer, audit
// persistence — against real providers. It is skipped unless
// TUTOR_LIVE_PROVIDER_CONFIRM=1, because it spends real provider quota.
//
// It exists because the compatibility probe proves what a provider does with a
// raw request, not what this application does with that provider.
func TestLiveTutorProviderEndToEnd(t *testing.T) {
	if os.Getenv("TUTOR_LIVE_PROVIDER_CONFIRM") != "1" {
		t.Fatal("build tag liveprovider was set without TUTOR_LIVE_PROVIDER_CONFIRM=1")
	}
	databaseURL := testDatabaseURL(t)
	ctx := context.Background()
	pool := isolatedPool(t, ctx, databaseURL)
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Audit rows reference a real student and session, so reuse the shared
	// fixture rather than inventing identifiers this schema would reject.
	fixture := seedSecurityFixture(t, ctx, pool)

	generator := liveChannel(t, "GENERATOR")
	reviewer := liveChannel(t, "REVIEWER")
	if generator.identity() == reviewer.identity() {
		t.Fatalf("generator and reviewer must differ: both %s", generator.identity())
	}
	for _, channel := range []liveProviderChannel{generator, reviewer} {
		if _, err := pool.Exec(ctx, `INSERT INTO ai_price_catalog
(id,provider,model,effective_from,input_price_per_million_usd,cached_input_price_per_million_usd,output_price_per_million_usd)
VALUES ($1,$2,$3,now()-interval '1 hour',1,0.5,2)`, uuid.New(), channel.provider, channel.model); err != nil {
			t.Fatalf("seed price for %s: %v", channel.identity(), err)
		}
	}

	generatorClient := liveClient(t, generator)
	reviewerClient := liveClient(t, reviewer)
	recorder := usage.NewRecorder(pool)
	independentReviewer, err := tutoraudit.NewOpenAIReviewer(
		reviewerClient.WithUsageRecorder(recorder), reviewer.provider, reviewer.model)
	if err != nil {
		t.Fatal(err)
	}
	retrying, err := tutoraudit.NewRetryingReviewer(independentReviewer)
	if err != nil {
		t.Fatal(err)
	}
	auditor, err := tutoraudit.NewService(
		generator.identity(), reviewer.identity(), retrying, tutoraudit.NewPostgresRecorder(pool))
	if err != nil {
		t.Fatal(err)
	}
	agent, err := ai.NewCodexProvider(generatorClient.WithUsageRecorder(recorder), auditor)
	if err != nil {
		t.Fatal(err)
	}

	request := ai.GenerateTurnRequest{
		StudentID: fixture.studentID.String(),
		SessionID: fixture.sessionID.String(),
		Question:  content.QuestionPublic{Prompt: "三杯同样的饮料一共 36 元，每杯多少元？"},
		AuditPrivateAnswer: content.QuestionPrivateAnswer{
			CorrectAnswer:          json.RawMessage(`{"value":"12元"}`),
			FullSolution:           "36 除以 3 等于 12。",
			TeacherReferenceAnswer: "每杯 12 元",
		},
		StudentAnswer: "我不知道怎么开始",
		TutorDecision: tutor.Decision{NextState: tutor.StateHint},
	}

	started := time.Now()
	turn, err := agent.GenerateTurn(ctx, request)
	elapsed := time.Since(started)

	// Both outcomes are correct production behaviour and both are asserted.
	// A published turn must clear every gate; a rejection must be fail closed,
	// which is what the V1.1 graded-material discipline exists to do when a
	// model answers a HINT with numeric material.
	switch {
	case err == nil:
		if strings.TrimSpace(turn.Message) == "" {
			t.Fatal("published turn has an empty message")
		}
		if turn.Action != tutor.StateHint {
			t.Fatalf("server-authorized action was not honoured: %q", turn.Action)
		}
		var audits int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM tutor_output_audits WHERE final_result='PASS'`).Scan(&audits); err != nil {
			t.Fatal(err)
		}
		if audits == 0 {
			t.Fatal("a turn was published without a passing independent audit")
		}
		t.Logf("PUBLISHED in %s: %q", elapsed, turn.Message)
	case errors.Is(err, ai.ErrTutorOutputRephraseRequired):
		if turn.Message != "" || len(turn.Segments) != 0 {
			t.Fatalf("rejected turn still carried publishable content: %+v", turn)
		}
		t.Logf("FAIL CLOSED in %s (expected for a HINT that introduces numeric material): %v", elapsed, err)
	default:
		t.Fatalf("live GenerateTurn failed after %s: %v", elapsed, err)
	}

	for _, leak := range []string{"12元", "12 元"} {
		if strings.Contains(turn.Message, leak) {
			t.Fatalf("live turn leaked the answer: %q", turn.Message)
		}
	}
	if elapsed >= 75*time.Second {
		t.Fatalf("generation plus review took %s, at or over the submit budget", elapsed)
	}
	var priced int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM ai_usage_records WHERE price_catalog_id IS NOT NULL`).Scan(&priced); err != nil {
		t.Fatal(err)
	}
	if priced < 2 {
		t.Fatalf("generation and review were not both priced and accounted: %d record(s)", priced)
	}
	var recorded int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tutor_output_audits`).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded == 0 {
		t.Fatal("no independent audit record was persisted")
	}
	t.Logf("live turn in %s via %s generating and %s reviewing", elapsed, generator.identity(), reviewer.identity())
}

type liveProviderChannel struct {
	provider, baseURL, apiKey, model, shape, overlay string
}

func (channel liveProviderChannel) identity() string { return channel.provider + ":" + channel.model }

func liveChannel(t *testing.T, role string) liveProviderChannel {
	t.Helper()
	read := func(suffix string) string {
		return strings.TrimSpace(os.Getenv("TUTOR_" + role + "_" + suffix))
	}
	channel := liveProviderChannel{
		provider: read("PROVIDER"), baseURL: read("BASE_URL"), apiKey: read("API_KEY"),
		model: read("MODEL"), shape: read("API_SHAPE"), overlay: read("REQUEST_OVERLAY"),
	}
	for name, value := range map[string]string{
		"PROVIDER": channel.provider, "BASE_URL": channel.baseURL,
		"API_KEY": channel.apiKey, "MODEL": channel.model, "API_SHAPE": channel.shape,
	} {
		if value == "" {
			t.Fatalf("TUTOR_%s_%s is required for the live provider test", role, name)
		}
	}
	return channel
}

func liveClient(t *testing.T, channel liveProviderChannel) *ai.StructuredProviderClient {
	t.Helper()
	var overlay map[string]any
	if channel.overlay != "" {
		if err := json.Unmarshal([]byte(channel.overlay), &overlay); err != nil {
			t.Fatalf("%s request overlay: %v", channel.identity(), err)
		}
	}
	client, err := ai.NewStructuredProviderClient(
		&http.Client{Timeout: 90 * time.Second}, channel.baseURL, channel.apiKey,
		channel.provider, channel.model, channel.shape, overlay)
	if err != nil {
		t.Fatalf("%s client: %v", channel.identity(), err)
	}
	return client
}
