package classroom

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

func scoreStageTask(task stageTask, response json.RawMessage) StageScore {
	if !validStageTask(task) {
		return StageScoreIndeterminate
	}
	rule, ok := decodeExactOptionSetRule(task.ScoringRule)
	if !ok {
		return StageScoreIndeterminate
	}
	var submitted selectionResponse
	if !strictJSONDecode(response, &submitted) || emptyOrDuplicate(submitted.SelectedOptionIDs) {
		return StageScoreIndeterminate
	}
	allowed := make(map[string]struct{}, len(rule.AllowedOptionIDs))
	for _, id := range rule.AllowedOptionIDs {
		allowed[id] = struct{}{}
	}
	for _, id := range submitted.SelectedOptionIDs {
		if _, ok := allowed[id]; !ok {
			return StageScoreIndeterminate
		}
	}
	expected := slices.Clone(rule.ExpectedOptionIDs)
	selected := slices.Clone(submitted.SelectedOptionIDs)
	slices.Sort(expected)
	slices.Sort(selected)
	if slices.Equal(expected, selected) {
		return StageScoreCorrect
	}
	return StageScoreIncorrect
}

func validStageTask(task stageTask) bool {
	if !task.SubjectCode.Valid() || !task.Stage.Valid() || task.ScoringRuleVersion != stageScoringExactOptionSetV1 {
		return false
	}
	_, ok := decodeExactOptionSetRule(task.ScoringRule)
	return ok
}

func decodeExactOptionSetRule(contents json.RawMessage) (exactOptionSetRule, bool) {
	var rule exactOptionSetRule
	if !strictJSONDecode(contents, &rule) || rule.RuleType != "EXACT_OPTION_SET" ||
		emptyOrDuplicate(rule.ExpectedOptionIDs) || emptyOrDuplicate(rule.AllowedOptionIDs) || len(rule.ExpectedOptionIDs) == 0 {
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

func strictJSONDecode(contents []byte, target any) bool {
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

func emptyOrDuplicate(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			return true
		}
		if _, exists := seen[value]; exists {
			return true
		}
		seen[value] = struct{}{}
	}
	return false
}
