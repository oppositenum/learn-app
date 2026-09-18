package main

import "testing"

func contentEnv(extra map[string]string) environmentLookup {
	base := map[string]string{
		contentGeneratorProviderEnv: "doubao",
		contentGeneratorBaseURLEnv:  "https://ark.example/api/v3",
		contentGeneratorAPIKeyEnv:   "generator-key",
		contentGeneratorModelEnv:    "doubao-seed-2-1-pro-260628",
		contentGeneratorOverlayEnv:  `{"thinking":{"type":"disabled"}}`,
		contentReviewerProviderEnv:  "qwen",
		contentReviewerBaseURLEnv:   "https://dashscope.example/compatible-mode/v1",
		contentReviewerAPIKeyEnv:    "reviewer-key",
		contentReviewerModelEnv:     "qwen3.7-plus-2026-05-26",
		contentReviewerOverlayEnv:   `{"enable_thinking":false}`,
	}
	return func(name string) string {
		if value, ok := extra[name]; ok {
			return value
		}
		return base[name]
	}
}

func TestContentProviderConfigUsesDoubaoGeneratorAndQwenReviewer(t *testing.T) {
	config, err := loadContentProviderConfig(contentEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !config.Enabled {
		t.Fatal("content providers were not enabled")
	}
	if config.Generator.identity() != "doubao:doubao-seed-2-1-pro-260628" {
		t.Fatalf("generator identity=%q", config.Generator.identity())
	}
	if config.Reviewer.identity() != "qwen:qwen3.7-plus-2026-05-26" {
		t.Fatalf("reviewer identity=%q", config.Reviewer.identity())
	}
	// Both providers were measured enforcing the schema on Chat Completions and
	// ignoring it on Responses, so that is the default here rather than a value
	// each deployment has to remember.
	if config.Generator.Shape != "chat_completions" || config.Reviewer.Shape != "chat_completions" {
		t.Fatalf("shapes=%s/%s", config.Generator.Shape, config.Reviewer.Shape)
	}
	if config.Generator.RequestOverlay["thinking"] == nil {
		t.Fatalf("generator overlay=%v", config.Generator.RequestOverlay)
	}
	if config.Reviewer.RequestOverlay["enable_thinking"] != false {
		t.Fatalf("reviewer overlay=%v", config.Reviewer.RequestOverlay)
	}
}

func TestContentProviderConfigRefusesUnsafeConfigurations(t *testing.T) {
	for _, test := range []struct {
		name  string
		extra map[string]string
	}{
		{
			name: "identical generator and reviewer identity",
			extra: map[string]string{
				contentReviewerProviderEnv: "doubao",
				contentReviewerModelEnv:    "doubao-seed-2-1-pro-260628",
			},
		},
		{name: "missing generator model", extra: map[string]string{contentGeneratorModelEnv: ""}},
		{name: "missing reviewer key", extra: map[string]string{contentReviewerAPIKeyEnv: ""}},
		{name: "non-OpenAI provider without base URL", extra: map[string]string{contentGeneratorBaseURLEnv: ""}},
		{name: "unknown wire shape", extra: map[string]string{contentGeneratorShapeEnv: "grpc"}},
		{name: "overlay is not an object", extra: map[string]string{contentGeneratorOverlayEnv: `"disabled"`}},
		{name: "overlay overrides the model", extra: map[string]string{contentGeneratorOverlayEnv: `{"model":"cheaper"}`}},
		{name: "overlay overrides response_format", extra: map[string]string{contentReviewerOverlayEnv: `{"response_format":{"type":"text"}}`}},
		{name: "overlay overrides messages", extra: map[string]string{contentReviewerOverlayEnv: `{"messages":[]}`}},
		{name: "context cache enabled", extra: map[string]string{contentGeneratorCacheEnv: "true"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := loadContentProviderConfig(contentEnv(test.extra)); err == nil {
				t.Fatal("an unsafe content provider configuration was accepted")
			}
		})
	}
}

func TestRetiredOpenAIContentConfigFailsClosedInsteadOfDowngrading(t *testing.T) {
	// A deployment still carrying the retired variables must stop, not quietly
	// lose AI content generation or fall back to the OpenAI text path.
	for _, retired := range retiredContentEnvNames {
		t.Run(retired+" without new channels", func(t *testing.T) {
			getenv := func(name string) string {
				if name == retired {
					return "gpt-5.6-luna"
				}
				return ""
			}
			if _, err := loadContentProviderConfig(getenv); err == nil {
				t.Fatal("a retired OpenAI content variable was ignored")
			}
		})
		t.Run(retired+" alongside new channels", func(t *testing.T) {
			if _, err := loadContentProviderConfig(contentEnv(map[string]string{retired: "gpt-5.6-luna"})); err == nil {
				t.Fatal("a retired OpenAI content variable was left in place")
			}
		})
	}
}

func TestContentProvidersStayDisabledWhenUnconfigured(t *testing.T) {
	config, err := loadContentProviderConfig(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if config.Enabled {
		t.Fatal("content providers were enabled without configuration")
	}
}
