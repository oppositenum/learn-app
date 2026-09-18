package main

import (
	"fmt"
	"strings"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
)

const (
	contentGeneratorProviderEnv = "CONTENT_GENERATOR_PROVIDER"
	contentGeneratorBaseURLEnv  = "CONTENT_GENERATOR_BASE_URL"
	contentGeneratorAPIKeyEnv   = "CONTENT_GENERATOR_API_KEY"
	contentGeneratorModelEnv    = "CONTENT_GENERATOR_MODEL"
	contentGeneratorShapeEnv    = "CONTENT_GENERATOR_API_SHAPE"
	contentGeneratorOverlayEnv  = "CONTENT_GENERATOR_REQUEST_OVERLAY"
	contentGeneratorCacheEnv    = "CONTENT_GENERATOR_CONTEXT_CACHE"
	contentReviewerProviderEnv  = "CONTENT_REVIEWER_PROVIDER"
	contentReviewerBaseURLEnv   = "CONTENT_REVIEWER_BASE_URL"
	contentReviewerAPIKeyEnv    = "CONTENT_REVIEWER_API_KEY"
	contentReviewerModelEnv     = "CONTENT_REVIEWER_MODEL"
	contentReviewerShapeEnv     = "CONTENT_REVIEWER_API_SHAPE"
	contentReviewerOverlayEnv   = "CONTENT_REVIEWER_REQUEST_OVERLAY"
	contentReviewerCacheEnv     = "CONTENT_REVIEWER_CONTEXT_CACHE"
)

var contentChannelEnvNames = []string{
	contentGeneratorProviderEnv, contentGeneratorBaseURLEnv,
	contentGeneratorAPIKeyEnv, contentGeneratorModelEnv,
	contentReviewerProviderEnv, contentReviewerBaseURLEnv,
	contentReviewerAPIKeyEnv, contentReviewerModelEnv,
}

// retiredContentEnvNames configured the content pipeline through the OpenAI
// Responses client. They are refused rather than ignored: a deployment that
// still carries them would otherwise lose AI content generation silently at
// the next restart, and a silent downgrade is worse than a failed start.
var retiredContentEnvNames = []string{
	"OPENAI_CONTENT_GENERATION_MODEL",
	"OPENAI_CONTENT_REVIEW_MODEL",
}

type contentProviderConfig struct {
	Enabled   bool
	Generator providerChannelConfig
	Reviewer  providerChannelConfig
}

// loadContentProviderConfig resolves the content pipeline's generation and
// independent review channels. It mirrors loadTutorProviderConfig deliberately:
// both drive the same structured client, so a second set of configuration
// semantics would be a source of drift, not flexibility.
func loadContentProviderConfig(getenv environmentLookup) (contentProviderConfig, error) {
	if err := requireContextCacheDisabled(contentGeneratorCacheEnv, getenv(contentGeneratorCacheEnv)); err != nil {
		return contentProviderConfig{}, err
	}
	if err := requireContextCacheDisabled(contentReviewerCacheEnv, getenv(contentReviewerCacheEnv)); err != nil {
		return contentProviderConfig{}, err
	}

	configured := false
	for _, name := range contentChannelEnvNames {
		if strings.TrimSpace(getenv(name)) != "" {
			configured = true
			break
		}
	}
	for _, retired := range retiredContentEnvNames {
		if strings.TrimSpace(getenv(retired)) == "" {
			continue
		}
		if !configured {
			return contentProviderConfig{}, fmt.Errorf(
				"%s is retired; configure the %s and %s channels instead",
				retired, contentGeneratorProviderEnv, contentReviewerProviderEnv)
		}
		return contentProviderConfig{}, fmt.Errorf(
			"%s is retired and must be removed now that the content channels are configured", retired)
	}
	if !configured {
		return contentProviderConfig{}, nil
	}

	generatorOverlay, err := parseRequestOverlay(contentGeneratorOverlayEnv, getenv(contentGeneratorOverlayEnv))
	if err != nil {
		return contentProviderConfig{}, err
	}
	reviewerOverlay, err := parseRequestOverlay(contentReviewerOverlayEnv, getenv(contentReviewerOverlayEnv))
	if err != nil {
		return contentProviderConfig{}, err
	}

	config := contentProviderConfig{
		Enabled: true,
		Generator: providerChannelConfig{
			Provider:       strings.TrimSpace(getenv(contentGeneratorProviderEnv)),
			BaseURL:        strings.TrimSpace(getenv(contentGeneratorBaseURLEnv)),
			APIKey:         strings.TrimSpace(getenv(contentGeneratorAPIKeyEnv)),
			Model:          strings.TrimSpace(getenv(contentGeneratorModelEnv)),
			Shape:          valueOrDefault(getenv(contentGeneratorShapeEnv), ai.ShapeChatCompletions),
			RequestOverlay: generatorOverlay,
		},
		Reviewer: providerChannelConfig{
			Provider:       strings.TrimSpace(getenv(contentReviewerProviderEnv)),
			BaseURL:        strings.TrimSpace(getenv(contentReviewerBaseURLEnv)),
			APIKey:         strings.TrimSpace(getenv(contentReviewerAPIKeyEnv)),
			Model:          strings.TrimSpace(getenv(contentReviewerModelEnv)),
			Shape:          valueOrDefault(getenv(contentReviewerShapeEnv), ai.ShapeChatCompletions),
			RequestOverlay: reviewerOverlay,
		},
	}
	if err := validateProviderChannel("content generator", config.Generator); err != nil {
		return contentProviderConfig{}, err
	}
	if err := validateProviderChannel("content reviewer", config.Reviewer); err != nil {
		return contentProviderConfig{}, err
	}
	// Stage 4 of the content pipeline requires a reviewer independent of the
	// generator; NewReviewService refuses equal identities, and refusing here
	// makes the misconfiguration a startup failure rather than a runtime one.
	if config.Generator.identity() == config.Reviewer.identity() {
		return contentProviderConfig{}, fmt.Errorf(
			"content generator and reviewer must use different provider:model identities; both resolved to %q",
			config.Generator.identity())
	}
	return config, nil
}
