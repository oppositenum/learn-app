package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
)

const (
	tutorGeneratorProviderEnv = "TUTOR_GENERATOR_PROVIDER"
	tutorGeneratorBaseURLEnv  = "TUTOR_GENERATOR_BASE_URL"
	tutorGeneratorAPIKeyEnv   = "TUTOR_GENERATOR_API_KEY"
	tutorGeneratorModelEnv    = "TUTOR_GENERATOR_MODEL"
	tutorReviewerProviderEnv  = "TUTOR_REVIEWER_PROVIDER"
	tutorReviewerBaseURLEnv   = "TUTOR_REVIEWER_BASE_URL"
	tutorReviewerAPIKeyEnv    = "TUTOR_REVIEWER_API_KEY"
	tutorReviewerModelEnv     = "TUTOR_REVIEWER_MODEL"
	tutorGeneratorCacheEnv    = "TUTOR_GENERATOR_CONTEXT_CACHE"
	tutorReviewerCacheEnv     = "TUTOR_REVIEWER_CONTEXT_CACHE"
	tutorGeneratorShapeEnv    = "TUTOR_GENERATOR_API_SHAPE"
	tutorReviewerShapeEnv     = "TUTOR_REVIEWER_API_SHAPE"
	tutorGeneratorOverlayEnv  = "TUTOR_GENERATOR_REQUEST_OVERLAY"
	tutorReviewerOverlayEnv   = "TUTOR_REVIEWER_REQUEST_OVERLAY"
)

var tutorChannelEnvNames = []string{
	tutorGeneratorProviderEnv,
	tutorGeneratorBaseURLEnv,
	tutorGeneratorAPIKeyEnv,
	tutorGeneratorModelEnv,
	tutorReviewerProviderEnv,
	tutorReviewerBaseURLEnv,
	tutorReviewerAPIKeyEnv,
	tutorReviewerModelEnv,
}

// providerChannelConfig is one configured AI channel. Tutor and the content
// pipeline both use it so the two never drift into different configuration
// semantics for the same underlying client.
type providerChannelConfig struct {
	Provider string
	BaseURL  string
	APIKey   string
	Model    string
	// Shape is the provider wire format. It is per channel because the shape a
	// provider actually enforces the schema on is a property of that provider,
	// not of the project.
	Shape string
	// RequestOverlay carries provider controls such as disabling reasoning
	// mode. It is load bearing for the 75s submit budget: with reasoning left
	// on, a single generation has been measured at most of that budget.
	RequestOverlay map[string]any
}

func (config providerChannelConfig) identity() string {
	return config.Provider + ":" + config.Model
}

type tutorChannelConfig = providerChannelConfig

type tutorProviderConfig struct {
	Enabled   bool
	Generator tutorChannelConfig
	Reviewer  tutorChannelConfig
}

type environmentLookup func(string) string

func loadTutorProviderConfig(getenv environmentLookup) (tutorProviderConfig, error) {
	if err := requireContextCacheDisabled(tutorGeneratorCacheEnv, getenv(tutorGeneratorCacheEnv)); err != nil {
		return tutorProviderConfig{}, err
	}
	if err := requireContextCacheDisabled(tutorReviewerCacheEnv, getenv(tutorReviewerCacheEnv)); err != nil {
		return tutorProviderConfig{}, err
	}
	explicit := false
	for _, name := range tutorChannelEnvNames {
		if strings.TrimSpace(getenv(name)) != "" {
			explicit = true
			break
		}
	}

	legacyKey := strings.TrimSpace(getenv("OPENAI_API_KEY"))
	legacyBaseURL := strings.TrimSpace(getenv("OPENAI_BASE_URL"))
	legacyGeneratorModel := strings.TrimSpace(getenv("OPENAI_TUTOR_MODEL"))
	legacyReviewerModel := strings.TrimSpace(getenv("OPENAI_TUTOR_OUTPUT_REVIEW_MODEL"))
	if !explicit && (legacyKey == "" || legacyGeneratorModel == "") {
		return tutorProviderConfig{}, nil
	}

	generatorOverlay, err := parseRequestOverlay(tutorGeneratorOverlayEnv, getenv(tutorGeneratorOverlayEnv))
	if err != nil {
		return tutorProviderConfig{}, err
	}
	reviewerOverlay, err := parseRequestOverlay(tutorReviewerOverlayEnv, getenv(tutorReviewerOverlayEnv))
	if err != nil {
		return tutorProviderConfig{}, err
	}
	config := tutorProviderConfig{
		Enabled: true,
		Generator: tutorChannelConfig{
			Provider:       valueOrDefault(getenv(tutorGeneratorProviderEnv), "openai"),
			BaseURL:        valueOrDefault(getenv(tutorGeneratorBaseURLEnv), legacyBaseURL),
			APIKey:         valueOrDefault(getenv(tutorGeneratorAPIKeyEnv), legacyKey),
			Model:          valueOrDefault(getenv(tutorGeneratorModelEnv), legacyGeneratorModel),
			Shape:          valueOrDefault(getenv(tutorGeneratorShapeEnv), ai.ShapeChatCompletions),
			RequestOverlay: generatorOverlay,
		},
		Reviewer: tutorChannelConfig{
			Provider:       valueOrDefault(getenv(tutorReviewerProviderEnv), "openai"),
			BaseURL:        valueOrDefault(getenv(tutorReviewerBaseURLEnv), legacyBaseURL),
			APIKey:         valueOrDefault(getenv(tutorReviewerAPIKeyEnv), legacyKey),
			Model:          valueOrDefault(getenv(tutorReviewerModelEnv), legacyReviewerModel),
			Shape:          valueOrDefault(getenv(tutorReviewerShapeEnv), ai.ShapeChatCompletions),
			RequestOverlay: reviewerOverlay,
		},
	}
	if err := validateProviderChannel("Tutor generator", config.Generator); err != nil {
		return tutorProviderConfig{}, err
	}
	if err := requireTutorOverlay("Tutor generator", tutorGeneratorOverlayEnv, config.Generator); err != nil {
		return tutorProviderConfig{}, err
	}
	if err := validateProviderChannel("Tutor reviewer", config.Reviewer); err != nil {
		return tutorProviderConfig{}, err
	}
	if err := requireTutorOverlay("Tutor reviewer", tutorReviewerOverlayEnv, config.Reviewer); err != nil {
		return tutorProviderConfig{}, err
	}
	if config.Generator.identity() == config.Reviewer.identity() {
		return tutorProviderConfig{}, fmt.Errorf(
			"Tutor generator and reviewer must use different provider:model identities; both resolved to %q",
			config.Generator.identity(),
		)
	}
	return config, nil
}

func requireContextCacheDisabled(name, value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "0", "false", "off", "disabled":
		return nil
	case "1", "true", "on", "enabled":
		return fmt.Errorf("%s cannot be enabled: ai_price_catalog has no cache-write price field", name)
	default:
		return fmt.Errorf("%s must be false or unset", name)
	}
}

// parseRequestOverlay reads a JSON object of provider-specific request fields.
// It refuses anything that would rewrite the structured-output contract or the
// identity the call is priced and accounted under.
func parseRequestOverlay(name, value string) (map[string]any, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	var overlay map[string]any
	if err := json.Unmarshal([]byte(value), &overlay); err != nil {
		return nil, fmt.Errorf("%s must be a JSON object: %w", name, err)
	}
	if err := ai.ValidateRequestOverlay(overlay); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return overlay, nil
}

func validateProviderChannel(name string, config providerChannelConfig) error {
	if config.Provider == "" {
		return fmt.Errorf("%s provider is required", name)
	}
	if config.APIKey == "" {
		return fmt.Errorf("%s API key is required", name)
	}
	if config.Model == "" {
		return fmt.Errorf("%s model is required", name)
	}
	if config.Provider != "openai" && config.BaseURL == "" {
		return fmt.Errorf("%s base URL is required for non-OpenAI provider %q", name, config.Provider)
	}
	if strings.Contains(config.Provider, ":") {
		return fmt.Errorf("%s provider must not contain ':'", name)
	}
	switch config.Shape {
	case ai.ShapeResponses, ai.ShapeChatCompletions:
	default:
		return fmt.Errorf("%s API shape %q must be %q or %q", name, config.Shape, ai.ShapeResponses, ai.ShapeChatCompletions)
	}
	return nil
}

func requireTutorOverlay(name, envName string, config providerChannelConfig) error {
	if len(config.RequestOverlay) == 0 {
		return fmt.Errorf("%s request overlay is required (%s) so reasoning mode stays off the 75s submit budget", name, envName)
	}
	return nil
}

func valueOrDefault(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return strings.TrimSpace(fallback)
}
