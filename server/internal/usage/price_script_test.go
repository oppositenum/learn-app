package usage

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPriceCatalogSQLGeneratorConvertsCNYAndRecordsFXEvidence(t *testing.T) {
	script := filepath.Join("..", "..", "..", "scripts", "generate-ai-price-catalog-sql.sh")
	command := exec.Command(script,
		"--id", "11111111-1111-4111-8111-111111111111",
		"--provider", "provider-a",
		"--model", "model-a",
		"--effective-from", "2026-09-16T00:00:00+08:00",
		"--fx-cny-per-usd", "7",
		"--fx-source", "Approved FX source",
		"--fx-date", "2026-09-16",
		"--input-cny-per-million", "7",
		"--cached-input-cny-per-million", "3.5",
		"--output-cny-per-million", "14",
		"--audio-input-cny-per-minute", "0",
		"--audio-output-cny-per-minute", "0.7",
		"--cost-tier", "STANDARD",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generate SQL: %v\n%s", err, output)
	}
	text := string(output)
	for _, expected := range []string{
		"-- FX source: Approved FX source",
		"-- FX observation date: 2026-09-16",
		"-- FX rate (CNY per USD): 7",
		"1.000000000, 0.500000000, 2.000000000, 0.000000000, 0.100000000",
		"UPDATE ai_price_catalog",
		"INSERT INTO ai_price_catalog",
		"Context cache write is not represented",
	} {
		if !strings.Contains(text, expected) {
			t.Errorf("generated SQL does not contain %q\n%s", expected, text)
		}
	}
}

func TestPriceCatalogSQLGeneratorRejectsUnsafeIdentity(t *testing.T) {
	script := filepath.Join("..", "..", "..", "scripts", "generate-ai-price-catalog-sql.sh")
	command := exec.Command(script,
		"--id", "11111111-1111-4111-8111-111111111111",
		"--provider", "provider';drop-table",
		"--model", "model-a",
		"--effective-from", "2026-09-16T00:00:00Z",
		"--fx-cny-per-usd", "7",
		"--fx-source", "Approved source",
		"--fx-date", "2026-09-16",
		"--input-cny-per-million", "1",
		"--cached-input-cny-per-million", "0",
		"--output-cny-per-million", "1",
		"--audio-input-cny-per-minute", "0",
		"--audio-output-cny-per-minute", "0",
		"--cost-tier", "LOW",
	)
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("unsafe provider was accepted:\n%s", output)
	}
}
