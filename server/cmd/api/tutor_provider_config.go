package main

import (
	"errors"
	"fmt"
	"strings"
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

type tutorChannelConfig struct {
	Provider string
	BaseURL  string
	APIKey   string
	Model    string
}

func (config tutorChannelConfig) identity() string {
	return config.Provider + ":" + config.Model
}

type tutorProviderConfig struct {
	Enabled   bool
	Generator tutorChannelConfig
	Reviewer  tutorChannelConfig
}

type environmentLookup func(string) string

func loadTutorProviderConfig(getenv environmentLookup) (tutorProviderConfig, error) {
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

	config := tutorProviderConfig{
		Enabled: true,
		Generator: tutorChannelConfig{
			Provider: valueOrDefault(getenv(tutorGeneratorProviderEnv), "openai"),
			BaseURL:  valueOrDefault(getenv(tutorGeneratorBaseURLEnv), legacyBaseURL),
			APIKey:   valueOrDefault(getenv(tutorGeneratorAPIKeyEnv), legacyKey),
			Model:    valueOrDefault(getenv(tutorGeneratorModelEnv), legacyGeneratorModel),
		},
		Reviewer: tutorChannelConfig{
			Provider: valueOrDefault(getenv(tutorReviewerProviderEnv), "openai"),
			BaseURL:  valueOrDefault(getenv(tutorReviewerBaseURLEnv), legacyBaseURL),
			APIKey:   valueOrDefault(getenv(tutorReviewerAPIKeyEnv), legacyKey),
			Model:    valueOrDefault(getenv(tutorReviewerModelEnv), legacyReviewerModel),
		},
	}
	if err := validateTutorChannel("generator", config.Generator); err != nil {
		return tutorProviderConfig{}, err
	}
	if err := validateTutorChannel("reviewer", config.Reviewer); err != nil {
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

func validateTutorChannel(name string, config tutorChannelConfig) error {
	if config.Provider == "" {
		return fmt.Errorf("Tutor %s provider is required", name)
	}
	if config.APIKey == "" {
		return fmt.Errorf("Tutor %s API key is required", name)
	}
	if config.Model == "" {
		return fmt.Errorf("Tutor %s model is required", name)
	}
	if config.Provider != "openai" && config.BaseURL == "" {
		return fmt.Errorf("Tutor %s base URL is required for non-OpenAI provider %q", name, config.Provider)
	}
	if strings.Contains(config.Provider, ":") {
		return errors.New("Tutor provider must not contain ':'")
	}
	return nil
}

func valueOrDefault(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return strings.TrimSpace(fallback)
}
