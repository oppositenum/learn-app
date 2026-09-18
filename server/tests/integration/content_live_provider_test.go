//go:build liveprovider

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
	"github.com/oppositenum/ai-learning-tutor/server/internal/contentpipeline"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/internal/usage"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

// TestLiveContentPipelineSchemaCompatibility drives real providers through the
// production content pipeline path: the shared structured client, the real
// generator and reviewer, price preflight and usage recording against real
// PostgreSQL. It exists because the switch to Doubao and Qwen has only been
// proven under mocks, while content_generation.schema.json and
// content_review.schema.json use length limits, nested arrays and nested enums
// — the exact controls both providers were measured not enforcing.
//
// Sample size is deliberately small: this establishes whether the schemas are
// honoured at all, not a stable success rate.
func TestLiveContentPipelineSchemaCompatibility(t *testing.T) {
	if os.Getenv("CONTENT_LIVE_CONFIRM") != "1" {
		t.Fatal("build tag liveprovider was set without CONTENT_LIVE_CONFIRM=1")
	}
	databaseURL := testDatabaseURL(t)
	ctx := context.Background()
	pool := isolatedPool(t, ctx, databaseURL)
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	generatorChannel := liveContentChannel(t, "CONTENT_GENERATOR")
	reviewerChannel := liveContentChannel(t, "CONTENT_REVIEWER")
	if generatorChannel.identity() == reviewerChannel.identity() {
		t.Fatalf("generator and reviewer must differ: both %s", generatorChannel.identity())
	}
	for _, channel := range []liveContentProviderChannel{generatorChannel, reviewerChannel} {
		if _, err := pool.Exec(ctx, `INSERT INTO ai_price_catalog
(id,provider,model,effective_from,input_price_per_million_usd,cached_input_price_per_million_usd,output_price_per_million_usd)
VALUES ($1,$2,$3,now()-interval '1 hour',1,0.5,2)`, uuid.New(), channel.provider, channel.model); err != nil {
			t.Fatalf("seed price for %s: %v", channel.identity(), err)
		}
	}
	recorder := usage.NewRecorder(pool)

	generator, err := contentpipeline.NewOpenAIGenerator(
		liveContentClient(t, generatorChannel).WithUsageRecorder(recorder),
		generatorChannel.provider, generatorChannel.model)
	if err != nil {
		t.Fatal(err)
	}
	reviewer, err := contentpipeline.NewOpenAIReviewer(
		liveContentClient(t, reviewerChannel).WithUsageRecorder(recorder),
		reviewerChannel.provider, reviewerChannel.model)
	if err != nil {
		t.Fatal(err)
	}

	generationStarted := time.Now()
	result, metadata, generationErr := generator.Generate(ctx, contentpipeline.GenerationContext{
		KnowledgePointID:     uuid.NewString(),
		SubjectCode:          "MATH",
		SubjectName:          "数学",
		KnowledgePointCode:   "MATH-JUN-LINEAR-EQUATION",
		KnowledgePointName:   "一元一次方程",
		KnowledgeDescription: "用一个未知数表示未知量，并根据等量关系列出方程求解。",
		GradeBandCode:        "JUNIOR_SECONDARY",
		DomainName:           "代数",
		UnitName:             "方程与不等式",
		CurriculumSourceName: "互动式学习 V1 产品课程骨架",
		CurriculumSourceRef:  "docs/product/互动式学习_V1.md",
		WhyItMatters:         json.RawMessage(`{"daily_life":"算清一次消费里固定费用和单价的关系"}`),
		Difficulty:           "L2",
		QuestionType:         "FREE_TEXT",
		Count:                1,
		Requirements:         "使用一个生活化场景，不要照搬任何商业题库。",
		SourceName:           "互动式学习 V1 产品课程骨架",
		SourceType:           "INTERNAL_PRODUCT_SPEC",
		SourceLicense:        "INTERNAL",
		SourceAttribution:    "AI Learning Tutor 产品规格",
	})
	generationElapsed := time.Since(generationStarted)

	if generationErr != nil {
		t.Errorf("GENERATION REJECTED after %s by %s: %v",
			generationElapsed, generatorChannel.identity(), generationErr)
	} else {
		if len(result.Questions) != 1 {
			t.Errorf("generation returned %d question(s), asked for 1", len(result.Questions))
		}
		t.Logf("GENERATION ACCEPTED in %s by %s (request %s, questions=%d)",
			generationElapsed, generatorChannel.identity(), metadata.RequestID, len(result.Questions))
		for index, question := range result.Questions {
			t.Logf("  question[%d] prompt_len=%d answer_len=%d solution_len=%d choices=%d",
				index, len([]rune(question.PromptPublic)), len([]rune(question.Answer)),
				len([]rune(question.Solution)), len(question.Choices))
		}
	}

	// Review a fixed, self-authored asset so the review leg is exercised even
	// when generation is rejected, and so no generated text leaks between legs.
	reviewStarted := time.Now()
	review, evidence, reviewErr := reviewer.Review(ctx, contentpipeline.Asset{
		KnowledgePointID: uuid.NewString(),
		SubjectCode:      "MATH",
		Difficulty:       "L2",
		QuestionType:     "FREE_TEXT",
		PromptPublic:     "三张同价门票加 6 元服务费一共 36 元，每张门票多少元？",
		TeacherPrivate: contentpipeline.PrivateAnswer{
			Answer: "10 元", Solution: "先从 36 元里减去 6 元服务费，再把余下的平均分成三份。",
			Misconceptions: []string{},
		},
		InputSchema:    json.RawMessage(`{"type":"text"}`),
		ContentVersion: "live-probe",
	})
	reviewElapsed := time.Since(reviewStarted)

	if reviewErr != nil {
		t.Errorf("REVIEW REJECTED after %s by %s: %v",
			reviewElapsed, reviewerChannel.identity(), reviewErr)
	} else {
		t.Logf("REVIEW ACCEPTED in %s by %s (request %s, result=%s, findings=%d)",
			reviewElapsed, reviewerChannel.identity(), evidence.RequestID, review.Result, len(review.Findings))
	}

	rows, err := pool.Query(ctx, `
SELECT provider, model, purpose, latency_ms, price_catalog_id IS NOT NULL
FROM ai_usage_records ORDER BY created_at`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	accounted := 0
	for rows.Next() {
		var provider, model, purpose string
		var latency int64
		var priced bool
		if err := rows.Scan(&provider, &model, &purpose, &latency, &priced); err != nil {
			t.Fatal(err)
		}
		if !priced {
			t.Errorf("usage row for %s:%s/%s carries no price version", provider, model, purpose)
		}
		accounted++
		t.Logf("ACCOUNTED %s:%s purpose=%s latency=%dms priced=%v", provider, model, purpose, latency, priced)
	}
	if accounted == 0 {
		t.Error("no content pipeline call was accounted; price preflight may have blocked every request")
	}
}

type liveContentProviderChannel struct {
	provider, baseURL, apiKey, model, shape, overlay string
}

func (channel liveContentProviderChannel) identity() string {
	return channel.provider + ":" + channel.model
}

func liveContentChannel(t *testing.T, prefix string) liveContentProviderChannel {
	t.Helper()
	read := func(suffix string) string { return strings.TrimSpace(os.Getenv(prefix + "_" + suffix)) }
	channel := liveContentProviderChannel{
		provider: read("PROVIDER"), baseURL: read("BASE_URL"), apiKey: read("API_KEY"),
		model: read("MODEL"), shape: read("API_SHAPE"), overlay: read("REQUEST_OVERLAY"),
	}
	for name, value := range map[string]string{
		"PROVIDER": channel.provider, "BASE_URL": channel.baseURL,
		"API_KEY": channel.apiKey, "MODEL": channel.model, "API_SHAPE": channel.shape,
	} {
		if value == "" {
			t.Fatalf("%s_%s is required for the live content probe", prefix, name)
		}
	}
	return channel
}

func liveContentClient(t *testing.T, channel liveContentProviderChannel) *ai.StructuredProviderClient {
	t.Helper()
	var overlay map[string]any
	if channel.overlay != "" {
		if err := json.Unmarshal([]byte(channel.overlay), &overlay); err != nil {
			t.Fatalf("%s request overlay: %v", channel.identity(), err)
		}
	}
	client, err := ai.NewStructuredProviderClient(
		&http.Client{Timeout: 120 * time.Second}, channel.baseURL, channel.apiKey,
		channel.provider, channel.model, channel.shape, overlay)
	if err != nil {
		t.Fatalf("%s client: %v", channel.identity(), err)
	}
	return client
}
