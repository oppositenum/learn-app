package aioutputs

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	ProviderEvidenceRejected     = "PROVIDER_REJECTED"
	ProviderEvidenceConservative = "CONSERVATIVE_BLOCK"
	ProviderEvidenceUnverified   = "UNVERIFIED"
)

// oneOf is rejected by the deployed gateway. Related composition and
// conditional keywords are blocked conservatively; anyOf remains blocked only
// until this gateway has been tested with it.
var blockedProviderSchemaKeywords = map[string]string{
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

// BlockedProviderSchemaKeywords returns the structured-output keywords that
// require provider evidence before they may be sent to the configured gateway.
func BlockedProviderSchemaKeywords() map[string]string {
	blocked := make(map[string]string, len(blockedProviderSchemaKeywords))
	for keyword, evidence := range blockedProviderSchemaKeywords {
		blocked[keyword] = evidence
	}
	return blocked
}

// ValidateProviderSchemaCompatibility rejects blocked structured-output schema
// features before a provider request can be made.
func ValidateProviderSchemaCompatibility(raw []byte) error {
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		return fmt.Errorf("decode structured-output schema: %w", err)
	}
	return inspectSchema(document, "$")
}

func inspectSchema(value any, path string) error {
	schema, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	keys := sortedKeys(schema)
	for _, key := range keys {
		if evidence, blocked := blockedProviderSchemaKeywords[key]; blocked {
			return fmt.Errorf("structured-output schema keyword %q at %s is blocked (%s)", key, path, evidence)
		}
	}
	for _, key := range keys {
		childPath := path + "/" + escapePathToken(key)
		switch key {
		case "properties", "$defs", "definitions":
			children, ok := schema[key].(map[string]any)
			if !ok {
				continue
			}
			for _, name := range sortedKeys(children) {
				if err := inspectSchema(children[name], childPath+"/"+escapePathToken(name)); err != nil {
					return err
				}
			}
		case "items", "additionalProperties", "contains", "propertyNames", "unevaluatedProperties", "unevaluatedItems":
			if err := inspectSchema(schema[key], childPath); err != nil {
				return err
			}
		case "prefixItems":
			children, ok := schema[key].([]any)
			if !ok {
				continue
			}
			for index, child := range children {
				if err := inspectSchema(child, fmt.Sprintf("%s/%d", childPath, index)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func sortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func escapePathToken(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}
