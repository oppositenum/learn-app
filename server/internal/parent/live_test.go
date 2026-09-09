package parent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCurrentAnswerPreviewUsesUnicodeLimitAndRejectsMultilineText(t *testing.T) {
	tests := []struct {
		name       string
		answer     string
		visibility string
		visible    bool
	}{
		{name: "empty", visibility: "NONE"},
		{name: "eighty Unicode characters", answer: strings.Repeat("界", 80), visibility: "SHORT_CURRENT", visible: true},
		{name: "eighty one Unicode characters", answer: strings.Repeat("界", 81), visibility: "WITHHELD_LONG"},
		{name: "multiline", answer: "first\nsecond", visibility: "WITHHELD_LONG"},
		{name: "leading newline", answer: "\nshort", visibility: "WITHHELD_LONG"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			preview, visibility := currentAnswerPreview(test.answer)
			if visibility != test.visibility || (preview != nil) != test.visible {
				t.Fatalf("preview=%v visibility=%s want visible=%t visibility=%s", preview, visibility, test.visible, test.visibility)
			}
			if preview != nil && utf8.RuneCountInString(*preview) > parentCurrentAnswerLimit {
				t.Fatalf("preview contains %d Unicode characters", utf8.RuneCountInString(*preview))
			}
		})
	}
}

func TestParentTurnSummaryContainsNoOriginalMessage(t *testing.T) {
	action := "PROBE"
	if got := parentTurnSummary("STUDENT", nil); got != "孩子提交了一次回答" {
		t.Fatalf("Student summary=%q", got)
	}
	if got := parentTurnSummary("TUTOR", &action); got != "Tutor 完成了 PROBE 教学步骤" {
		t.Fatalf("Tutor summary=%q", got)
	}
}
