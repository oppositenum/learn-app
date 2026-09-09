package studentinteraction

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"reflect"
	"regexp"
	"strings"
)

const (
	Version             = "student-interaction-v1"
	TextFallbackVersion = "student-text-fallback-v1"
)

type Renderer string

const (
	RendererSingleChoice Renderer = "SINGLE_CHOICE"
	RendererMultiChoice  Renderer = "MULTIPLE_CHOICE"
	RendererOrdering     Renderer = "ORDERING"
	RendererMatching     Renderer = "MATCHING"
	RendererGrouping     Renderer = "GROUPING"
	RendererNumberLine   Renderer = "NUMBER_LINE"
	RendererFillBlanks   Renderer = "FILL_BLANKS"
	RendererTextFallback Renderer = "TEXT_FALLBACK"
)

type Item struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type NumberLine struct {
	Label string  `json:"label"`
	Min   float64 `json:"min"`
	Max   float64 `json:"max"`
	Step  float64 `json:"step"`
}

type Scene struct {
	Version            string      `json:"version"`
	Renderer           Renderer    `json:"renderer"`
	AccessibleFallback string      `json:"accessible_fallback"`
	Options            []Item      `json:"options,omitempty"`
	Items              []Item      `json:"items,omitempty"`
	Left               []Item      `json:"left,omitempty"`
	Right              []Item      `json:"right,omitempty"`
	Groups             []Item      `json:"groups,omitempty"`
	Slots              []Item      `json:"slots,omitempty"`
	NumberLine         *NumberLine `json:"number_line,omitempty"`
}

type Material struct {
	Version            string          `json:"version"`
	Renderer           Renderer        `json:"renderer"`
	Scene              *Scene          `json:"scene,omitempty"`
	AnswerSchema       json.RawMessage `json:"answer_schema"`
	AccessibleFallback string          `json:"accessible_fallback"`
	Fallback           bool            `json:"fallback"`
}

var publicIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

func Resolve(prompt string, rawScene, rawSchema json.RawMessage) Material {
	scene, ok := Parse(rawScene, rawSchema)
	if ok {
		return Material{
			Version: Version, Renderer: scene.Renderer, Scene: &scene,
			AnswerSchema: rawSchema, AccessibleFallback: scene.AccessibleFallback,
		}
	}
	fallback := strings.TrimSpace(prompt)
	if fallback == "" {
		fallback = "请使用文字回答当前任务。"
	}
	return Material{
		Version: TextFallbackVersion, Renderer: RendererTextFallback,
		AnswerSchema:       json.RawMessage(`{"type":"string","minLength":1,"maxLength":2000}`),
		AccessibleFallback: fallback, Fallback: true,
	}
}

func Valid(rawScene, rawSchema json.RawMessage) bool {
	_, ok := Parse(rawScene, rawSchema)
	return ok
}

func Parse(rawScene, rawSchema json.RawMessage) (Scene, bool) {
	var scene Scene
	if !strictDecode(rawScene, &scene) || !validScene(scene) {
		return Scene{}, false
	}
	expected, ok := AnswerSchema(scene)
	if !ok || !sameJSON(expected, rawSchema) {
		return Scene{}, false
	}
	return scene, true
}

func AnswerSchema(scene Scene) (json.RawMessage, bool) {
	if !validScene(scene) {
		return nil, false
	}
	ids := func(items []Item) []string {
		values := make([]string, 0, len(items))
		for _, item := range items {
			values = append(values, item.ID)
		}
		return values
	}
	arrayOfIDs := func(values []string, minItems, maxItems int) map[string]any {
		return map[string]any{
			"type": "array", "items": map[string]any{"type": "string", "enum": values},
			"minItems": minItems, "maxItems": maxItems, "uniqueItems": true,
		}
	}
	object := func(key string, property any) map[string]any {
		return map[string]any{
			"type": "object", "properties": map[string]any{key: property},
			"required": []string{key}, "additionalProperties": false,
		}
	}
	var schema map[string]any
	switch scene.Renderer {
	case RendererSingleChoice:
		schema = object("selected_option_ids", arrayOfIDs(ids(scene.Options), 1, 1))
	case RendererMultiChoice:
		schema = object("selected_option_ids", arrayOfIDs(ids(scene.Options), 1, len(scene.Options)))
	case RendererOrdering:
		schema = object("ordered_item_ids", arrayOfIDs(ids(scene.Items), len(scene.Items), len(scene.Items)))
	case RendererMatching:
		pair := map[string]any{
			"type": "object",
			"properties": map[string]any{
				"left_id":  map[string]any{"type": "string", "enum": ids(scene.Left)},
				"right_id": map[string]any{"type": "string", "enum": ids(scene.Right)},
			},
			"required": []string{"left_id", "right_id"}, "additionalProperties": false,
		}
		schema = object("pairs", map[string]any{
			"type": "array", "items": pair, "minItems": len(scene.Left),
			"maxItems": len(scene.Left), "uniqueItems": true,
		})
	case RendererGrouping:
		placement := map[string]any{
			"type": "object",
			"properties": map[string]any{
				"item_id":  map[string]any{"type": "string", "enum": ids(scene.Items)},
				"group_id": map[string]any{"type": "string", "enum": ids(scene.Groups)},
			},
			"required": []string{"item_id", "group_id"}, "additionalProperties": false,
		}
		schema = object("placements", map[string]any{
			"type": "array", "items": placement, "minItems": len(scene.Items),
			"maxItems": len(scene.Items), "uniqueItems": true,
		})
	case RendererNumberLine:
		schema = object("value", map[string]any{
			"type": "number", "minimum": scene.NumberLine.Min,
			"maximum": scene.NumberLine.Max, "multipleOf": scene.NumberLine.Step,
		})
	case RendererFillBlanks:
		value := map[string]any{
			"type": "object",
			"properties": map[string]any{
				"slot_id": map[string]any{"type": "string", "enum": ids(scene.Slots)},
				"value":   map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
			},
			"required": []string{"slot_id", "value"}, "additionalProperties": false,
		}
		schema = object("values", map[string]any{
			"type": "array", "items": value, "minItems": len(scene.Slots),
			"maxItems": len(scene.Slots), "uniqueItems": true,
		})
	default:
		return nil, false
	}
	encoded, err := json.Marshal(schema)
	return encoded, err == nil
}

func validScene(scene Scene) bool {
	if scene.Version != Version || strings.TrimSpace(scene.AccessibleFallback) == "" || len([]rune(scene.AccessibleFallback)) > 1200 {
		return false
	}
	validItems := func(items []Item, minimum int) bool {
		if len(items) < minimum || len(items) > 30 {
			return false
		}
		seen := make(map[string]struct{}, len(items))
		for _, item := range items {
			if !publicIDPattern.MatchString(item.ID) || strings.TrimSpace(item.Label) == "" || len([]rune(item.Label)) > 200 {
				return false
			}
			if _, exists := seen[item.ID]; exists {
				return false
			}
			seen[item.ID] = struct{}{}
		}
		return true
	}
	noOtherCollections := func(allowed ...string) bool {
		present := map[string]bool{
			"options": len(scene.Options) > 0, "items": len(scene.Items) > 0,
			"left": len(scene.Left) > 0, "right": len(scene.Right) > 0,
			"groups": len(scene.Groups) > 0, "slots": len(scene.Slots) > 0,
			"number_line": scene.NumberLine != nil,
		}
		for _, name := range allowed {
			delete(present, name)
		}
		for _, exists := range present {
			if exists {
				return false
			}
		}
		return true
	}
	switch scene.Renderer {
	case RendererSingleChoice, RendererMultiChoice:
		return validItems(scene.Options, 2) && noOtherCollections("options")
	case RendererOrdering:
		return validItems(scene.Items, 2) && noOtherCollections("items")
	case RendererMatching:
		return validItems(scene.Left, 1) && validItems(scene.Right, 1) && len(scene.Left) == len(scene.Right) && noOtherCollections("left", "right")
	case RendererGrouping:
		return validItems(scene.Items, 1) && validItems(scene.Groups, 2) && noOtherCollections("items", "groups")
	case RendererNumberLine:
		if scene.NumberLine == nil || strings.TrimSpace(scene.NumberLine.Label) == "" || len([]rune(scene.NumberLine.Label)) > 200 {
			return false
		}
		line := scene.NumberLine
		steps := (line.Max - line.Min) / line.Step
		minimumSteps := line.Min / line.Step
		return line.Step > 0 && line.Max > line.Min && steps <= 200 && math.Abs(steps-math.Round(steps)) < 1e-9 &&
			math.Abs(minimumSteps-math.Round(minimumSteps)) < 1e-9 && noOtherCollections("number_line")
	case RendererFillBlanks:
		return validItems(scene.Slots, 1) && noOtherCollections("slots")
	default:
		return false
	}
}

func sameJSON(left, right []byte) bool {
	var leftValue, rightValue any
	if !strictDecode(left, &leftValue) || !strictDecode(right, &rightValue) {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
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
