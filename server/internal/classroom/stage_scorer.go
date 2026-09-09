package classroom

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"slices"

	"github.com/oppositenum/ai-learning-tutor/server/internal/studentinteraction"
)

type exactOptionSetRule struct {
	RuleType          string   `json:"rule_type"`
	ExpectedOptionIDs []string `json:"expected_option_ids"`
	AllowedOptionIDs  []string `json:"allowed_option_ids"`
}

type exactOrderRule struct {
	RuleType        string   `json:"rule_type"`
	ExpectedItemIDs []string `json:"expected_item_ids"`
	AllowedItemIDs  []string `json:"allowed_item_ids"`
}

type exactPair struct {
	LeftID  string `json:"left_id"`
	RightID string `json:"right_id"`
}

type exactPairsRule struct {
	RuleType        string      `json:"rule_type"`
	ExpectedPairs   []exactPair `json:"expected_pairs"`
	AllowedLeftIDs  []string    `json:"allowed_left_ids"`
	AllowedRightIDs []string    `json:"allowed_right_ids"`
}

type exactPlacement struct {
	ItemID  string `json:"item_id"`
	GroupID string `json:"group_id"`
}

type exactGroupingRule struct {
	RuleType        string           `json:"rule_type"`
	Expected        []exactPlacement `json:"expected_placements"`
	AllowedItemIDs  []string         `json:"allowed_item_ids"`
	AllowedGroupIDs []string         `json:"allowed_group_ids"`
}

type exactNumberRule struct {
	RuleType      string  `json:"rule_type"`
	ExpectedValue float64 `json:"expected_value"`
	Min           float64 `json:"min"`
	Max           float64 `json:"max"`
	Step          float64 `json:"step"`
}

type acceptedFillValue struct {
	SlotID         string   `json:"slot_id"`
	AcceptedValues []string `json:"accepted_values"`
}

type exactFillRule struct {
	RuleType       string              `json:"rule_type"`
	ExpectedValues []acceptedFillValue `json:"expected_values"`
	AllowedSlotIDs []string            `json:"allowed_slot_ids"`
}

type selectionResponse struct {
	SelectedOptionIDs []string `json:"selected_option_ids"`
}

type orderResponse struct {
	OrderedItemIDs []string `json:"ordered_item_ids"`
}

type pairsResponse struct {
	Pairs []exactPair `json:"pairs"`
}

type groupingResponse struct {
	Placements []exactPlacement `json:"placements"`
}

type numberResponse struct {
	Value float64 `json:"value"`
}

type submittedFillValue struct {
	SlotID string `json:"slot_id"`
	Value  string `json:"value"`
}

type fillResponse struct {
	Values []submittedFillValue `json:"values"`
}

func scoreStageTask(task stageTask, response json.RawMessage) StageScore {
	scene, ok := validStageTaskScene(task)
	if !ok {
		return StageScoreIndeterminate
	}
	switch task.ScoringRuleVersion {
	case stageScoringExactOptionSetV1:
		return scoreOptionSet(task.ScoringRule, response, scene.Renderer)
	case stageScoringExactOrderV1:
		return scoreOrder(task.ScoringRule, response)
	case stageScoringExactPairsV1:
		return scorePairs(task.ScoringRule, response)
	case stageScoringExactGroupingV1:
		return scoreGrouping(task.ScoringRule, response)
	case stageScoringExactNumberV1:
		return scoreNumber(task.ScoringRule, response)
	case stageScoringExactFillV1:
		return scoreFill(task.ScoringRule, response)
	default:
		return StageScoreIndeterminate
	}
}

func validStageTask(task stageTask) bool {
	_, ok := validStageTaskScene(task)
	return ok
}

func validStageTaskScene(task stageTask) (studentinteraction.Scene, bool) {
	if !task.SubjectCode.Valid() || !task.Stage.Valid() {
		return studentinteraction.Scene{}, false
	}
	scene, ok := studentinteraction.Parse(task.Scene, task.InputSchema)
	if !ok {
		return studentinteraction.Scene{}, false
	}
	ids := func(items []studentinteraction.Item) []string {
		values := make([]string, 0, len(items))
		for _, item := range items {
			values = append(values, item.ID)
		}
		return values
	}
	switch task.ScoringRuleVersion {
	case stageScoringExactOptionSetV1:
		rule, valid := decodeExactOptionSetRule(task.ScoringRule)
		return scene, valid && (scene.Renderer == studentinteraction.RendererSingleChoice || scene.Renderer == studentinteraction.RendererMultiChoice) && sameSet(rule.AllowedOptionIDs, ids(scene.Options))
	case stageScoringExactOrderV1:
		rule, valid := decodeExactOrderRule(task.ScoringRule)
		return scene, valid && scene.Renderer == studentinteraction.RendererOrdering && sameSet(rule.AllowedItemIDs, ids(scene.Items))
	case stageScoringExactPairsV1:
		rule, valid := decodeExactPairsRule(task.ScoringRule)
		return scene, valid && scene.Renderer == studentinteraction.RendererMatching && sameSet(rule.AllowedLeftIDs, ids(scene.Left)) && sameSet(rule.AllowedRightIDs, ids(scene.Right))
	case stageScoringExactGroupingV1:
		rule, valid := decodeExactGroupingRule(task.ScoringRule)
		return scene, valid && scene.Renderer == studentinteraction.RendererGrouping && sameSet(rule.AllowedItemIDs, ids(scene.Items)) && sameSet(rule.AllowedGroupIDs, ids(scene.Groups))
	case stageScoringExactNumberV1:
		rule, valid := decodeExactNumberRule(task.ScoringRule)
		return scene, valid && scene.Renderer == studentinteraction.RendererNumberLine && nearlyEqual(rule.Min, scene.NumberLine.Min) && nearlyEqual(rule.Max, scene.NumberLine.Max) && nearlyEqual(rule.Step, scene.NumberLine.Step)
	case stageScoringExactFillV1:
		rule, valid := decodeExactFillRule(task.ScoringRule)
		return scene, valid && scene.Renderer == studentinteraction.RendererFillBlanks && sameSet(rule.AllowedSlotIDs, ids(scene.Slots))
	default:
		return studentinteraction.Scene{}, false
	}
}

func scoreOptionSet(rawRule, response json.RawMessage, renderer studentinteraction.Renderer) StageScore {
	rule, ok := decodeExactOptionSetRule(rawRule)
	var submitted selectionResponse
	if !ok || !strictJSONDecode(response, &submitted) || len(submitted.SelectedOptionIDs) == 0 || emptyOrDuplicate(submitted.SelectedOptionIDs) ||
		(renderer == studentinteraction.RendererSingleChoice && len(submitted.SelectedOptionIDs) != 1) {
		return StageScoreIndeterminate
	}
	if !subset(submitted.SelectedOptionIDs, rule.AllowedOptionIDs) {
		return StageScoreIndeterminate
	}
	if sameSet(rule.ExpectedOptionIDs, submitted.SelectedOptionIDs) {
		return StageScoreCorrect
	}
	return StageScoreIncorrect
}

func scoreOrder(rawRule, response json.RawMessage) StageScore {
	rule, ok := decodeExactOrderRule(rawRule)
	var submitted orderResponse
	if !ok || !strictJSONDecode(response, &submitted) || emptyOrDuplicate(submitted.OrderedItemIDs) || !sameSet(submitted.OrderedItemIDs, rule.AllowedItemIDs) {
		return StageScoreIndeterminate
	}
	if slices.Equal(rule.ExpectedItemIDs, submitted.OrderedItemIDs) {
		return StageScoreCorrect
	}
	return StageScoreIncorrect
}

func scorePairs(rawRule, response json.RawMessage) StageScore {
	rule, ok := decodeExactPairsRule(rawRule)
	var submitted pairsResponse
	if !ok || !strictJSONDecode(response, &submitted) || !validPairs(submitted.Pairs, rule.AllowedLeftIDs, rule.AllowedRightIDs) {
		return StageScoreIndeterminate
	}
	if samePairs(rule.ExpectedPairs, submitted.Pairs) {
		return StageScoreCorrect
	}
	return StageScoreIncorrect
}

func scoreGrouping(rawRule, response json.RawMessage) StageScore {
	rule, ok := decodeExactGroupingRule(rawRule)
	var submitted groupingResponse
	if !ok || !strictJSONDecode(response, &submitted) || !validPlacements(submitted.Placements, rule.AllowedItemIDs, rule.AllowedGroupIDs) {
		return StageScoreIndeterminate
	}
	if samePlacements(rule.Expected, submitted.Placements) {
		return StageScoreCorrect
	}
	return StageScoreIncorrect
}

func scoreNumber(rawRule, response json.RawMessage) StageScore {
	rule, ok := decodeExactNumberRule(rawRule)
	var submitted numberResponse
	if !ok || !strictJSONDecode(response, &submitted) || !validNumber(submitted.Value, rule.Min, rule.Max, rule.Step) {
		return StageScoreIndeterminate
	}
	if nearlyEqual(rule.ExpectedValue, submitted.Value) {
		return StageScoreCorrect
	}
	return StageScoreIncorrect
}

func scoreFill(rawRule, response json.RawMessage) StageScore {
	rule, ok := decodeExactFillRule(rawRule)
	var submitted fillResponse
	if !ok || !strictJSONDecode(response, &submitted) || len(submitted.Values) != len(rule.AllowedSlotIDs) {
		return StageScoreIndeterminate
	}
	values := make(map[string]string, len(submitted.Values))
	for _, value := range submitted.Values {
		if value.SlotID == "" || value.Value == "" || !contains(rule.AllowedSlotIDs, value.SlotID) {
			return StageScoreIndeterminate
		}
		if _, duplicate := values[value.SlotID]; duplicate {
			return StageScoreIndeterminate
		}
		values[value.SlotID] = value.Value
	}
	for _, expected := range rule.ExpectedValues {
		actual, found := values[expected.SlotID]
		if !found {
			return StageScoreIndeterminate
		}
		accepted := false
		for _, candidate := range expected.AcceptedValues {
			accepted = accepted || normalized(actual) == normalized(candidate)
		}
		if !accepted {
			return StageScoreIncorrect
		}
	}
	return StageScoreCorrect
}

func decodeExactOptionSetRule(contents []byte) (exactOptionSetRule, bool) {
	var rule exactOptionSetRule
	if !strictJSONDecode(contents, &rule) || rule.RuleType != "EXACT_OPTION_SET" ||
		emptyOrDuplicate(rule.ExpectedOptionIDs) || emptyOrDuplicate(rule.AllowedOptionIDs) || len(rule.ExpectedOptionIDs) == 0 ||
		!subset(rule.ExpectedOptionIDs, rule.AllowedOptionIDs) {
		return exactOptionSetRule{}, false
	}
	return rule, true
}

func decodeExactOrderRule(contents []byte) (exactOrderRule, bool) {
	var rule exactOrderRule
	if !strictJSONDecode(contents, &rule) || rule.RuleType != "EXACT_ORDER" || emptyOrDuplicate(rule.ExpectedItemIDs) ||
		emptyOrDuplicate(rule.AllowedItemIDs) || !sameSet(rule.ExpectedItemIDs, rule.AllowedItemIDs) {
		return exactOrderRule{}, false
	}
	return rule, true
}

func decodeExactPairsRule(contents []byte) (exactPairsRule, bool) {
	var rule exactPairsRule
	if !strictJSONDecode(contents, &rule) || rule.RuleType != "EXACT_PAIRS" ||
		!validPairs(rule.ExpectedPairs, rule.AllowedLeftIDs, rule.AllowedRightIDs) {
		return exactPairsRule{}, false
	}
	return rule, true
}

func decodeExactGroupingRule(contents []byte) (exactGroupingRule, bool) {
	var rule exactGroupingRule
	if !strictJSONDecode(contents, &rule) || rule.RuleType != "EXACT_GROUPING" ||
		!validPlacements(rule.Expected, rule.AllowedItemIDs, rule.AllowedGroupIDs) {
		return exactGroupingRule{}, false
	}
	return rule, true
}

func decodeExactNumberRule(contents []byte) (exactNumberRule, bool) {
	var rule exactNumberRule
	if !strictJSONDecode(contents, &rule) || rule.RuleType != "EXACT_NUMBER" ||
		!validNumber(rule.ExpectedValue, rule.Min, rule.Max, rule.Step) {
		return exactNumberRule{}, false
	}
	return rule, true
}

func decodeExactFillRule(contents []byte) (exactFillRule, bool) {
	var rule exactFillRule
	if !strictJSONDecode(contents, &rule) || rule.RuleType != "EXACT_FILL" || emptyOrDuplicate(rule.AllowedSlotIDs) ||
		len(rule.ExpectedValues) != len(rule.AllowedSlotIDs) {
		return exactFillRule{}, false
	}
	seen := make(map[string]struct{}, len(rule.ExpectedValues))
	for _, expected := range rule.ExpectedValues {
		if !contains(rule.AllowedSlotIDs, expected.SlotID) || len(expected.AcceptedValues) == 0 {
			return exactFillRule{}, false
		}
		if _, duplicate := seen[expected.SlotID]; duplicate {
			return exactFillRule{}, false
		}
		seen[expected.SlotID] = struct{}{}
		for _, value := range expected.AcceptedValues {
			if normalized(value) == "" {
				return exactFillRule{}, false
			}
		}
	}
	return rule, true
}

func validPairs(pairs []exactPair, allowedLeft, allowedRight []string) bool {
	if emptyOrDuplicate(allowedLeft) || emptyOrDuplicate(allowedRight) || len(allowedLeft) != len(allowedRight) || len(pairs) != len(allowedLeft) {
		return false
	}
	leftSeen, rightSeen := map[string]struct{}{}, map[string]struct{}{}
	for _, pair := range pairs {
		if !contains(allowedLeft, pair.LeftID) || !contains(allowedRight, pair.RightID) {
			return false
		}
		if _, duplicate := leftSeen[pair.LeftID]; duplicate {
			return false
		}
		if _, duplicate := rightSeen[pair.RightID]; duplicate {
			return false
		}
		leftSeen[pair.LeftID], rightSeen[pair.RightID] = struct{}{}, struct{}{}
	}
	return true
}

func validPlacements(placements []exactPlacement, allowedItems, allowedGroups []string) bool {
	if emptyOrDuplicate(allowedItems) || emptyOrDuplicate(allowedGroups) || len(placements) != len(allowedItems) {
		return false
	}
	seen := map[string]struct{}{}
	for _, placement := range placements {
		if !contains(allowedItems, placement.ItemID) || !contains(allowedGroups, placement.GroupID) {
			return false
		}
		if _, duplicate := seen[placement.ItemID]; duplicate {
			return false
		}
		seen[placement.ItemID] = struct{}{}
	}
	return true
}

func validNumber(value, minimum, maximum, step float64) bool {
	if math.IsNaN(value) || math.IsInf(value, 0) || step <= 0 || maximum <= minimum || value < minimum || value > maximum {
		return false
	}
	steps := (value - minimum) / step
	return nearlyEqual(steps, math.Round(steps))
}

func samePairs(left, right []exactPair) bool {
	toMap := func(values []exactPair) map[string]string {
		result := make(map[string]string, len(values))
		for _, value := range values {
			result[value.LeftID] = value.RightID
		}
		return result
	}
	return mapsEqual(toMap(left), toMap(right))
}

func samePlacements(left, right []exactPlacement) bool {
	toMap := func(values []exactPlacement) map[string]string {
		result := make(map[string]string, len(values))
		for _, value := range values {
			result[value.ItemID] = value.GroupID
		}
		return result
	}
	return mapsEqual(toMap(left), toMap(right))
}

func mapsEqual(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func subset(values, allowed []string) bool {
	for _, value := range values {
		if !contains(allowed, value) {
			return false
		}
	}
	return true
}

func sameSet(left, right []string) bool {
	if len(left) != len(right) || emptyOrDuplicate(left) || emptyOrDuplicate(right) {
		return false
	}
	return subset(left, right)
}

func contains(values []string, value string) bool {
	return slices.Contains(values, value)
}

func nearlyEqual(left, right float64) bool {
	return math.Abs(left-right) <= 1e-9*math.Max(1, math.Max(math.Abs(left), math.Abs(right)))
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
