package studentinteraction

import (
	"encoding/json"
	"testing"
)

func TestAllV1RenderersBuildAndParseTheirBoundSchema(t *testing.T) {
	t.Parallel()
	items := []Item{{ID: "a", Label: "甲"}, {ID: "b", Label: "乙"}}
	tests := []Scene{
		{Version: Version, Renderer: RendererSingleChoice, AccessibleFallback: "从甲和乙中选择一项。", Options: items},
		{Version: Version, Renderer: RendererMultiChoice, AccessibleFallback: "从甲和乙中选择一项或多项。", Options: items},
		{Version: Version, Renderer: RendererOrdering, AccessibleFallback: "把甲和乙排成顺序。", Items: items},
		{Version: Version, Renderer: RendererMatching, AccessibleFallback: "为每个左侧项目选择右侧项目。", Left: items, Right: []Item{{ID: "x", Label: "一"}, {ID: "y", Label: "二"}}},
		{Version: Version, Renderer: RendererGrouping, AccessibleFallback: "把甲和乙分别归类。", Items: items, Groups: []Item{{ID: "x", Label: "第一组"}, {ID: "y", Label: "第二组"}}},
		{Version: Version, Renderer: RendererNumberLine, AccessibleFallback: "在零到二之间选择数值。", NumberLine: &NumberLine{Label: "数值", Min: 0, Max: 2, Step: 0.5}},
		{Version: Version, Renderer: RendererFillBlanks, AccessibleFallback: "填写甲和乙两个空格。", Slots: items},
	}
	for _, scene := range tests {
		scene := scene
		t.Run(string(scene.Renderer), func(t *testing.T) {
			rawScene, err := json.Marshal(scene)
			if err != nil {
				t.Fatal(err)
			}
			rawSchema, ok := AnswerSchema(scene)
			if !ok {
				t.Fatal("expected a schema")
			}
			parsed, ok := Parse(rawScene, rawSchema)
			if !ok || parsed.Renderer != scene.Renderer {
				t.Fatalf("parsed=%+v ok=%v", parsed, ok)
			}
			material := Resolve("题目", rawScene, rawSchema)
			if material.Fallback || material.Version != Version || material.Renderer != scene.Renderer {
				t.Fatalf("material=%+v", material)
			}
		})
	}
}

func TestUnknownIncompleteOrUnboundMaterialFailsClosedToText(t *testing.T) {
	t.Parallel()
	valid := Scene{
		Version: Version, Renderer: RendererSingleChoice,
		AccessibleFallback: "请选择一项。", Options: []Item{{ID: "a", Label: "甲"}, {ID: "b", Label: "乙"}},
	}
	validScene, _ := json.Marshal(valid)
	validSchema, _ := AnswerSchema(valid)
	tests := []struct {
		name   string
		scene  json.RawMessage
		schema json.RawMessage
	}{
		{name: "unknown version", scene: json.RawMessage(`{"version":"student-interaction-v2","renderer":"SINGLE_CHOICE","accessible_fallback":"请选择一项。","options":[{"id":"a","label":"甲"},{"id":"b","label":"乙"}]}`), schema: validSchema},
		{name: "unknown renderer", scene: json.RawMessage(`{"version":"student-interaction-v1","renderer":"DRAG_ANYTHING","accessible_fallback":"请选择一项。"}`), schema: validSchema},
		{name: "missing fallback", scene: json.RawMessage(`{"version":"student-interaction-v1","renderer":"SINGLE_CHOICE","options":[{"id":"a","label":"甲"},{"id":"b","label":"乙"}]}`), schema: validSchema},
		{name: "schema not bound to options", scene: validScene, schema: json.RawMessage(`{"type":"object","properties":{"selected_option_ids":{"type":"array","items":{"type":"string"}}},"required":["selected_option_ids"],"additionalProperties":false}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if Valid(test.scene, test.schema) {
				t.Fatal("invalid material was accepted")
			}
			material := Resolve("保留的文字任务", test.scene, test.schema)
			if !material.Fallback || material.Renderer != RendererTextFallback || material.AccessibleFallback != "保留的文字任务" || material.Scene != nil {
				t.Fatalf("material=%+v", material)
			}
		})
	}
}
