package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oppositenum/ai-learning-tutor/server/internal/usage"
)

func main() {
	if os.Getenv("PROVIDER_PROBE_CONFIRM_LIVE") != "1" {
		log.Fatal("live provider probing is disabled; set PROVIDER_PROBE_CONFIRM_LIVE=1 only after configuring isolated credentials, PostgreSQL, and effective price rows")
	}
	databaseURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if databaseURL == "" {
		log.Fatal("TEST_DATABASE_URL is required so every probe request can be price-checked and accounted")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		log.Fatalf("create probe database pool: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(context.Background()); err != nil {
		log.Fatalf("connect probe database: %v", err)
	}

	samples, err := positiveIntEnv("PROVIDER_PROBE_SAMPLES", 20)
	if err != nil {
		log.Fatal(err)
	}
	configs := []providerProbeConfig{
		providerConfigFromEnv("DOUBAO"),
		providerConfigFromEnv("QWEN"),
	}
	for _, config := range configs {
		if err := config.validate(); err != nil {
			log.Fatal(err)
		}
	}
	schemas, err := loadRuntimeSchemas("schemas/ai_outputs")
	if err != nil {
		log.Fatalf("load runtime schemas: %v", err)
	}

	// A full run is roughly 31 fixed calls plus four latency series per provider
	// plus two end-to-end directions. The previous samples*20+30 ceiling could
	// not cover that even before the reasoning-on comparison series was added.
	timeoutSeconds, err := positiveIntEnv("PROVIDER_PROBE_TIMEOUT_SECONDS", 1800+samples*120)
	if err != nil {
		log.Fatal(err)
	}
	runner := newProbeRunner(&http.Client{Timeout: 75 * time.Second}, usage.NewRecorder(pool), samples)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	reports := make(map[string]providerCompatibilityReport, len(configs))
	for _, config := range configs {
		report, err := runner.runProvider(ctx, config, schemas)
		if err != nil {
			log.Fatalf("probe %s: %v", config.Name, err)
		}
		reports[config.Name] = report
	}
	reports["doubao_to_qwen"] = runner.runDirection(ctx, configs[0], configs[1], schemas)
	reports["qwen_to_doubao"] = runner.runDirection(ctx, configs[1], configs[0], schemas)

	outputDirectory := strings.TrimSpace(os.Getenv("PROVIDER_PROBE_OUTPUT_DIR"))
	if outputDirectory == "" {
		outputDirectory = filepath.Join("tmp", "provider-compat")
	}
	if err := os.MkdirAll(outputDirectory, 0o700); err != nil {
		log.Fatalf("create output directory: %v", err)
	}
	outputPath := filepath.Join(outputDirectory, "probe-"+time.Now().UTC().Format("20060102T150405Z")+".json")
	contents, err := json.MarshalIndent(reports, "", "  ")
	if err != nil {
		log.Fatalf("encode probe report: %v", err)
	}
	if err := os.WriteFile(outputPath, append(contents, '\n'), 0o600); err != nil {
		log.Fatalf("write probe report: %v", err)
	}
	fmt.Println(outputPath)
}

// defaultReasoningControls are the request parameters observed to turn a
// provider's reasoning mode off on 2026-09-16. They are defaults for
// convenience, not a claim about the provider: set
// PROVIDER_PROBE_<NAME>_REASONING_CONTROL to override, or to "none" to sample
// the provider's own default.
var defaultReasoningControls = map[string]string{
	"doubao": `{"thinking":{"type":"disabled"}}`,
	"qwen":   `{"enable_thinking":false}`,
}

func providerConfigFromEnv(prefix string) providerProbeConfig {
	name := strings.ToLower(prefix)
	reasoning := strings.TrimSpace(os.Getenv("PROVIDER_PROBE_" + prefix + "_REASONING_CONTROL"))
	switch {
	case reasoning == "":
		reasoning = defaultReasoningControls[name]
	case strings.EqualFold(reasoning, "none"):
		reasoning = ""
	}
	return providerProbeConfig{
		Name:             name,
		Provider:         strings.TrimSpace(os.Getenv("PROVIDER_PROBE_" + prefix + "_PROVIDER")),
		BaseURL:          strings.TrimRight(strings.TrimSpace(os.Getenv("PROVIDER_PROBE_"+prefix+"_BASE_URL")), "/"),
		APIKey:           strings.TrimSpace(os.Getenv("PROVIDER_PROBE_" + prefix + "_API_KEY")),
		Model:            strings.TrimSpace(os.Getenv("PROVIDER_PROBE_" + prefix + "_MODEL")),
		Region:           strings.TrimSpace(os.Getenv("PROVIDER_PROBE_" + prefix + "_REGION")),
		ReasoningControl: reasoning,
		AuthHeader:       valueOr(strings.TrimSpace(os.Getenv("PROVIDER_PROBE_"+prefix+"_AUTH_HEADER")), "Authorization"),
		AuthPrefix:       valueOr(os.Getenv("PROVIDER_PROBE_"+prefix+"_AUTH_PREFIX"), "Bearer "),
	}
}

func (config providerProbeConfig) validate() error {
	for label, value := range map[string]string{
		"provider": config.Provider, "base URL": config.BaseURL, "API key": config.APIKey,
		"model": config.Model, "region": config.Region,
	} {
		if value == "" {
			return fmt.Errorf("%s probe %s is required", config.Name, label)
		}
	}
	if !strings.HasPrefix(config.BaseURL, "https://") {
		return fmt.Errorf("%s probe base URL must use https", config.Name)
	}
	if _, err := config.reasoningOverlay(); err != nil {
		return err
	}
	return nil
}

func positiveIntEnv(name string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return value, nil
}

func valueOr(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
