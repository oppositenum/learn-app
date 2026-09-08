package aioutputs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestProviderSchemaCompatibilityRejectsEveryBlockedKeyword(t *testing.T) {
	blocked := BlockedProviderSchemaKeywords()
	want := map[string]string{
		"oneOf":             ProviderEvidenceRejected,
		"allOf":             ProviderEvidenceConservative,
		"not":               ProviderEvidenceConservative,
		"if":                ProviderEvidenceConservative,
		"then":              ProviderEvidenceConservative,
		"else":              ProviderEvidenceConservative,
		"patternProperties": ProviderEvidenceConservative,
		"dependencies":      ProviderEvidenceConservative,
		"dependentSchemas":  ProviderEvidenceConservative,
		"dependentRequired": ProviderEvidenceConservative,
		"anyOf":             ProviderEvidenceUnverified,
	}
	if !reflect.DeepEqual(blocked, want) {
		t.Fatalf("blocked keywords=%v want=%v", blocked, want)
	}
	keywords := make([]string, 0, len(blocked))
	for keyword := range blocked {
		keywords = append(keywords, keyword)
	}
	sort.Strings(keywords)
	for _, keyword := range keywords {
		t.Run(keyword, func(t *testing.T) {
			raw := []byte(fmt.Sprintf(`{"type":"object","properties":{"value":{%q:{}}}}`, keyword))
			if err := ValidateProviderSchemaCompatibility(raw); err == nil {
				t.Fatalf("keyword %q was accepted", keyword)
			}
		})
	}
}

func TestProviderSchemaCompatibilityDoesNotTreatPropertyNamesAsKeywords(t *testing.T) {
	raw := []byte(`{"type":"object","properties":{"if":{"type":"string"},"then":{"type":"string"},"not":{"type":"string"}}}`)
	if err := ValidateProviderSchemaCompatibility(raw); err != nil {
		t.Fatalf("ordinary property name was treated as a schema keyword: %v", err)
	}
}

func TestAllEmbeddedStructuredOutputSchemasAreProviderCompatible(t *testing.T) {
	entries, err := fs.ReadDir(Files, ".")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".schema.json") {
			continue
		}
		count++
		raw, err := fs.ReadFile(Files, entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateProviderSchemaCompatibility(raw); err != nil {
			t.Errorf("%s: %v", entry.Name(), err)
		}
	}
	if count != 5 {
		t.Fatalf("embedded schema count=%d want=5; update the compatibility evidence for new schemas", count)
	}
}

func TestExistingStructuredOutputSchemasRemainProviderCompatible(t *testing.T) {
	working := []string{
		"analyze_answer.schema.json",
		"content_generation.schema.json",
		"content_review.schema.json",
		"tutor_turn.schema.json",
	}
	for _, filename := range working {
		t.Run(filename, func(t *testing.T) {
			raw, err := fs.ReadFile(Files, filename)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateProviderSchemaCompatibility(raw); err != nil {
				t.Fatalf("working schema was rejected: %v", err)
			}
		})
	}

	markers := map[string][][]byte{
		"analyze_answer.schema.json": {[]byte(`"minimum"`), []byte(`"maximum"`)},
		"tutor_turn.schema.json":     {[]byte(`"minLength"`), []byte(`"maxLength"`)},
	}
	for filename, expected := range markers {
		raw, err := fs.ReadFile(Files, filename)
		if err != nil {
			t.Fatal(err)
		}
		for _, marker := range expected {
			if !bytes.Contains(raw, marker) {
				t.Fatalf("%s no longer exercises accepted keyword %s", filename, marker)
			}
		}
	}
}

func TestTutorOutputReviewSchemaIsProviderCompatible(t *testing.T) {
	raw, err := fs.ReadFile(Files, "tutor_output_review.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateProviderSchemaCompatibility(raw); err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{
		"type": true, "properties": true, "required": true,
		"additionalProperties": true, "enum": true, "items": true,
	}
	var inspect func(any, bool)
	inspect = func(value any, propertyMap bool) {
		switch current := value.(type) {
		case map[string]any:
			for key, child := range current {
				if !propertyMap && !allowed[key] {
					t.Errorf("Tutor output review schema uses non-allowlisted keyword %q", key)
				}
				inspect(child, key == "properties")
			}
		case []any:
			for _, child := range current {
				inspect(child, false)
			}
		}
	}
	inspect(document, false)
}
