package classroompilot

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"
)

type exactOptionSetRule struct {
	RuleType          string   `json:"rule_type"`
	ExpectedOptionIDs []string `json:"expected_option_ids"`
	AllowedOptionIDs  []string `json:"allowed_option_ids"`
}

type selectionResponse struct {
	SelectedOptionIDs []string `json:"selected_option_ids"`
}

func Score(task Task, response json.RawMessage) DeterministicResult {
	if !task.SubjectCode.Valid() || !task.Stage.Valid() || task.ScoringRuleVersion != ScoringRuleExactOptionSetV1 {
		return ResultIndeterminate
	}
	rule, ok := decodeRule(task.ScoringRule)
	if !ok {
		return ResultIndeterminate
	}
	var submitted selectionResponse
	if !strictDecode(response, &submitted) || hasEmptyOrDuplicate(submitted.SelectedOptionIDs) {
		return ResultIndeterminate
	}
	allowed := make(map[string]struct{}, len(rule.AllowedOptionIDs))
	for _, id := range rule.AllowedOptionIDs {
		allowed[id] = struct{}{}
	}
	for _, id := range submitted.SelectedOptionIDs {
		if _, ok := allowed[id]; !ok {
			return ResultIndeterminate
		}
	}
	expected := slices.Clone(rule.ExpectedOptionIDs)
	selected := slices.Clone(submitted.SelectedOptionIDs)
	slices.Sort(expected)
	slices.Sort(selected)
	if slices.Equal(expected, selected) {
		return ResultCorrect
	}
	return ResultIncorrect
}

func validTask(task Task) bool {
	if !task.SubjectCode.Valid() || !task.Stage.Valid() || task.ScoringRuleVersion != ScoringRuleExactOptionSetV1 {
		return false
	}
	_, ok := decodeRule(task.ScoringRule)
	return ok
}

func decodeRule(contents json.RawMessage) (exactOptionSetRule, bool) {
	var rule exactOptionSetRule
	if !strictDecode(contents, &rule) || rule.RuleType != "EXACT_OPTION_SET" ||
		hasEmptyOrDuplicate(rule.ExpectedOptionIDs) || hasEmptyOrDuplicate(rule.AllowedOptionIDs) ||
		len(rule.ExpectedOptionIDs) == 0 {
		return exactOptionSetRule{}, false
	}
	allowed := make(map[string]struct{}, len(rule.AllowedOptionIDs))
	for _, id := range rule.AllowedOptionIDs {
		allowed[id] = struct{}{}
	}
	for _, id := range rule.ExpectedOptionIDs {
		if _, ok := allowed[id]; !ok {
			return exactOptionSetRule{}, false
		}
	}
	return rule, true
}

func strictDecode(contents []byte, target any) bool {
	if len(contents) == 0 {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return false
	}
	return decoder.Decode(&struct{}{}) == io.EOF
}

func hasEmptyOrDuplicate(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			return true
		}
		if _, ok := seen[value]; ok {
			return true
		}
		seen[value] = struct{}{}
	}
	return false
}
