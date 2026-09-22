package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRetiredOpenAITutorConfigFailsClosedInsteadOfFallingBack(t *testing.T) {
	for _, retired := range retiredTutorEnvNames {
		replacement := retiredTutorReplacement(retired)
		t.Run(retired+" without Tutor channels", func(t *testing.T) {
			_, err := loadTutorProviderConfig(mapLookup(map[string]string{
				"OPENAI_API_KEY": "used-by-speech-only",
				retired:          "legacy-model",
			}))
			if err == nil || !strings.Contains(err.Error(), retired) || !strings.Contains(err.Error(), replacement) {
				t.Fatalf("retired %s error=%v", retired, err)
			}
		})
		t.Run(retired+" alongside Tutor channels", func(t *testing.T) {
			_, err := loadTutorProviderConfig(mapLookup(map[string]string{
				tutorGeneratorProviderEnv: "doubao",
				tutorGeneratorBaseURLEnv:  "https://ark.example/api/v3",
				tutorGeneratorAPIKeyEnv:   "gk",
				tutorGeneratorModelEnv:    "doubao-pro",
				tutorReviewerProviderEnv:  "qwen",
				tutorReviewerBaseURLEnv:   "https://dashscope.example/v1",
				tutorReviewerAPIKeyEnv:    "rk",
				tutorReviewerModelEnv:     "qwen-plus",
				tutorGeneratorOverlayEnv:  `{"thinking":{"type":"disabled"}}`,
				tutorReviewerOverlayEnv:   `{"enable_thinking":false}`,
				retired:                   "legacy-model",
			}))
			if err == nil || !strings.Contains(err.Error(), retired) || !strings.Contains(err.Error(), replacement) {
				t.Fatalf("retired %s alongside channels error=%v", retired, err)
			}
		})
	}
}

func TestTutorProviderConfigEnablesFromTutorChannelsOnly(t *testing.T) {
	config, err := loadTutorProviderConfig(mapLookup(map[string]string{
		tutorGeneratorProviderEnv: "openai",
		tutorGeneratorAPIKeyEnv:   "gk",
		tutorGeneratorModelEnv:    "gpt-tutor",
		tutorReviewerProviderEnv:  "openai",
		tutorReviewerAPIKeyEnv:    "rk",
		tutorReviewerModelEnv:     "gpt-review",
		tutorGeneratorOverlayEnv:  `{"reasoning":{"effort":"low"}}`,
		tutorReviewerOverlayEnv:   `{"reasoning":{"effort":"low"}}`,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !config.Enabled {
		t.Fatal("Tutor channels did not enable Tutor")
	}
	if config.Generator.identity() != "openai:gpt-tutor" || config.Reviewer.identity() != "openai:gpt-review" {
		t.Fatalf("channel identities generator=%q reviewer=%q", config.Generator.identity(), config.Reviewer.identity())
	}
}

func TestTutorProviderConfigSupportsIndependentChannels(t *testing.T) {
	values := map[string]string{
		tutorGeneratorProviderEnv: "provider-a",
		tutorGeneratorBaseURLEnv:  "https://generator.example/v1",
		tutorGeneratorAPIKeyEnv:   "generator-key",
		tutorGeneratorModelEnv:    "generator-model",
		tutorReviewerProviderEnv:  "provider-b",
		tutorReviewerBaseURLEnv:   "https://reviewer.example/v1",
		tutorReviewerAPIKeyEnv:    "reviewer-key",
		tutorReviewerModelEnv:     "reviewer-model",
		tutorGeneratorOverlayEnv:  `{"thinking":{"type":"disabled"}}`,
		tutorReviewerOverlayEnv:   `{"enable_thinking":false}`,
	}
	config, err := loadTutorProviderConfig(mapLookup(values))
	if err != nil {
		t.Fatal(err)
	}
	if config.Generator.identity() != "provider-a:generator-model" || config.Reviewer.identity() != "provider-b:reviewer-model" {
		t.Fatalf("independent identities generator=%q reviewer=%q", config.Generator.identity(), config.Reviewer.identity())
	}
	if config.Generator.APIKey == config.Reviewer.APIKey || config.Generator.BaseURL == config.Reviewer.BaseURL {
		t.Fatalf("channels were not independently configured: %+v", config)
	}
}

func TestTutorProviderConfigRejectsSameIdentityAtStartup(t *testing.T) {
	values := map[string]string{
		tutorGeneratorProviderEnv: "provider-a",
		tutorGeneratorBaseURLEnv:  "https://generator.example/v1",
		tutorGeneratorAPIKeyEnv:   "generator-key",
		tutorGeneratorModelEnv:    "same-model",
		tutorReviewerProviderEnv:  "provider-a",
		tutorReviewerBaseURLEnv:   "https://reviewer.example/v1",
		tutorReviewerAPIKeyEnv:    "reviewer-key",
		tutorReviewerModelEnv:     "same-model",
		tutorGeneratorOverlayEnv:  `{"thinking":{"type":"disabled"}}`,
		tutorReviewerOverlayEnv:   `{"enable_thinking":false}`,
	}
	_, err := loadTutorProviderConfig(mapLookup(values))
	if err == nil || !strings.Contains(err.Error(), "different provider:model identities") || !strings.Contains(err.Error(), "provider-a:same-model") {
		t.Fatalf("same identity error=%v", err)
	}
}

func TestTutorProviderConfigRequiresCompleteExplicitChannels(t *testing.T) {
	_, err := loadTutorProviderConfig(mapLookup(map[string]string{
		tutorGeneratorProviderEnv: "provider-a",
	}))
	if err == nil || !strings.Contains(err.Error(), "generator API key is required") {
		t.Fatalf("incomplete channel error=%v", err)
	}
}

func TestTutorProviderConfigStaysDisabledWhenUnconfigured(t *testing.T) {
	config, err := loadTutorProviderConfig(mapLookup(map[string]string{
		"OPENAI_API_KEY":  "used-by-speech-only",
		"OPENAI_BASE_URL": "https://api.openai.com/v1",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if config.Enabled {
		t.Fatalf("speech-only OpenAI variables enabled Tutor: %+v", config)
	}
}

func TestTutorProviderContextCacheDefaultsOffAndCannotBeEnabled(t *testing.T) {
	if config, err := loadTutorProviderConfig(mapLookup(nil)); err != nil || config.Enabled {
		t.Fatalf("default configuration=%+v error=%v", config, err)
	}
	for _, name := range []string{tutorGeneratorCacheEnv, tutorReviewerCacheEnv} {
		_, err := loadTutorProviderConfig(mapLookup(map[string]string{name: "true"}))
		if err == nil || !strings.Contains(err.Error(), "no cache-write price field") {
			t.Fatalf("%s enable error=%v", name, err)
		}
	}
}

func TestTutorProviderSecretsRemainServerOnly(t *testing.T) {
	root := filepath.Join("..", "..", "..", "apps", "web")
	for _, subtree := range []string{"src", "dist"} {
		path := filepath.Join(root, subtree)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(path, func(filename string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || strings.HasSuffix(filename, ".map") {
				return nil
			}
			contents, err := os.ReadFile(filename)
			if err != nil {
				return err
			}
			for _, forbidden := range []string{
				tutorGeneratorBaseURLEnv, tutorGeneratorAPIKeyEnv,
				tutorReviewerBaseURLEnv, tutorReviewerAPIKeyEnv,
				"OPENAI_API_KEY",
			} {
				if strings.Contains(string(contents), forbidden) {
					t.Errorf("server-only provider setting %s found in %s", forbidden, filename)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func mapLookup(values map[string]string) environmentLookup {
	return func(name string) string { return values[name] }
}

func TestTutorProviderConfigShapeAndOverlay(t *testing.T) {
	base := map[string]string{
		"TUTOR_GENERATOR_PROVIDER": "doubao", "TUTOR_GENERATOR_BASE_URL": "https://ark.example/api/v3",
		"TUTOR_GENERATOR_API_KEY": "gk", "TUTOR_GENERATOR_MODEL": "doubao-pro",
		"TUTOR_REVIEWER_PROVIDER": "qwen", "TUTOR_REVIEWER_BASE_URL": "https://dashscope.example/v1",
		"TUTOR_REVIEWER_API_KEY": "rk", "TUTOR_REVIEWER_MODEL": "qwen-plus",
		"TUTOR_GENERATOR_REQUEST_OVERLAY": `{"thinking":{"type":"disabled"}}`,
		"TUTOR_REVIEWER_REQUEST_OVERLAY":  `{"enable_thinking":false}`,
	}
	with := func(extra map[string]string) func(string) string {
		return func(name string) string {
			if value, ok := extra[name]; ok {
				return value
			}
			return base[name]
		}
	}

	t.Run("shape and overlay reach both channels", func(t *testing.T) {
		config, err := loadTutorProviderConfig(with(map[string]string{
			"TUTOR_GENERATOR_API_SHAPE":       "chat_completions",
			"TUTOR_GENERATOR_REQUEST_OVERLAY": `{"thinking":{"type":"disabled"}}`,
			"TUTOR_REVIEWER_API_SHAPE":        "chat_completions",
			"TUTOR_REVIEWER_REQUEST_OVERLAY":  `{"enable_thinking":false}`,
		}))
		if err != nil {
			t.Fatal(err)
		}
		if config.Generator.Shape != "chat_completions" || config.Reviewer.Shape != "chat_completions" {
			t.Fatalf("shapes=%s/%s", config.Generator.Shape, config.Reviewer.Shape)
		}
		if config.Generator.RequestOverlay["thinking"] == nil || config.Reviewer.RequestOverlay["enable_thinking"] != false {
			t.Fatalf("overlays=%v/%v", config.Generator.RequestOverlay, config.Reviewer.RequestOverlay)
		}
	})

	t.Run("default shape is chat_completions so schema is enforced", func(t *testing.T) {
		config, err := loadTutorProviderConfig(with(nil))
		if err != nil {
			t.Fatal(err)
		}
		if config.Generator.Shape != "chat_completions" || config.Reviewer.Shape != "chat_completions" {
			t.Fatalf("default shapes=%s/%s", config.Generator.Shape, config.Reviewer.Shape)
		}
	})
	t.Run("empty overlay is refused", func(t *testing.T) {
		_, err := loadTutorProviderConfig(with(map[string]string{
			"TUTOR_GENERATOR_REQUEST_OVERLAY": "",
		}))
		if err == nil || !strings.Contains(err.Error(), "request overlay is required") || !strings.Contains(err.Error(), "TUTOR_GENERATOR_REQUEST_OVERLAY") {
			t.Fatalf("empty overlay error=%v", err)
		}
	})
	t.Run("missing reviewer overlay names the variable", func(t *testing.T) {
		_, err := loadTutorProviderConfig(with(map[string]string{
			"TUTOR_REVIEWER_REQUEST_OVERLAY": "",
		}))
		if err == nil || !strings.Contains(err.Error(), "TUTOR_REVIEWER_REQUEST_OVERLAY") {
			t.Fatalf("empty overlay error=%v", err)
		}
	})

	for _, test := range []struct {
		name  string
		extra map[string]string
	}{
		{name: "unknown shape", extra: map[string]string{"TUTOR_GENERATOR_API_SHAPE": "grpc"}},
		{name: "overlay is not an object", extra: map[string]string{"TUTOR_GENERATOR_REQUEST_OVERLAY": `"disabled"`}},
		{name: "overlay rewrites the contract", extra: map[string]string{"TUTOR_REVIEWER_REQUEST_OVERLAY": `{"response_format":{"type":"text"}}`}},
		{name: "overlay rewrites the model", extra: map[string]string{"TUTOR_GENERATOR_REQUEST_OVERLAY": `{"model":"cheaper"}`}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := loadTutorProviderConfig(with(test.extra)); err == nil {
				t.Fatal("unsafe Tutor provider configuration was accepted")
			}
		})
	}
}
