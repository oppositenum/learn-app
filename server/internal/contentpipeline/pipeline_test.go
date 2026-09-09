package contentpipeline

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/oppositenum/ai-learning-tutor/server/internal/studentinteraction"
)

func number(value float64) *float64 { return &value }
func validAsset() Asset {
	return Asset{
		QuestionID: "q1", KnowledgePointID: "kp1", SubjectCode: "PHYSICS", Difficulty: "L2", QuestionType: "FREE_TEXT",
		PromptPublic: "一辆车2小时行驶120千米，平均速度是多少？", InputSchema: json.RawMessage(`{"type":"number"}`),
		TeacherPrivate: PrivateAnswer{Answer: "60千米/时", NumericValue: number(60), Unit: "千米/时", Solution: "120除以2得到每小时60千米", SolutionResult: number(60)},
		SourceID:       "source1", ContentVersion: "v1", SchemaVersion: CurrentSchemaVersion, Status: Draft,
	}
}

func TestDeterministicValidationAndReleaseGate(t *testing.T) {
	asset := validAsset()
	validation := (Validator{}).Validate(asset)
	if !validation.Passed {
		t.Fatalf("valid asset rejected: %+v", validation)
	}
	gate := Gate{}
	state, err := gate.AfterValidation(Draft, validation)
	if err != nil || state != AutomaticValidated {
		t.Fatalf("validation state = %s, %v", state, err)
	}
	review := Review{Result: ReviewPass, AgeAppropriate: true, FactuallySound: true, Unambiguous: true, NoAnswerLeak: true, SafeValues: true}
	state, err = gate.AfterReview(state, validation, review, CurrentSchemaVersion, CurrentSchemaVersion, CurrentSchemaVersion)
	if err != nil || state != AIReviewed {
		t.Fatalf("review state = %s, %v", state, err)
	}
	state, err = gate.Release(state, validation, review)
	if err != nil || state != Released {
		t.Fatalf("release state = %s, %v", state, err)
	}
	state, err = gate.Quarantine(state, "parent reported ambiguous wording")
	if err != nil || state != Quarantined {
		t.Fatalf("quarantine state = %s, %v", state, err)
	}
}

func TestValidatorRejectsWrongNumericAnswerAndAnswerLeak(t *testing.T) {
	asset := validAsset()
	asset.PromptPublic += " 答案是60千米/时。"
	asset.TeacherPrivate.SolutionResult = number(50)
	validation := (Validator{}).Validate(asset)
	if validation.Passed {
		t.Fatalf("unsafe asset passed: %+v", validation)
	}
	state, err := (Gate{}).AfterValidation(Draft, validation)
	if err != nil || state != RejectedAutomatic {
		t.Fatalf("rejected state = %s, %v", state, err)
	}
}

func TestReleaseCannotSkipIndependentReview(t *testing.T) {
	validation := (Validator{}).Validate(validAsset())
	if _, err := (Gate{}).Release(AutomaticValidated, validation, Review{Result: ReviewPass}); err == nil {
		t.Fatal("release skipped AI_REVIEWED state")
	}
	if _, err := NewReviewService("openai:model-a", "openai:model-a", fakeReviewer{}); err == nil {
		t.Fatal("same generator and reviewer accepted")
	}
}

func TestStructuredInteractionRequiresTheVersionedPublicContract(t *testing.T) {
	scene := studentinteraction.Scene{
		Version: studentinteraction.Version, Renderer: studentinteraction.RendererSingleChoice,
		AccessibleFallback: "从两个选项中选择一项。",
		Options:            []studentinteraction.Item{{ID: "a", Label: "甲"}, {ID: "b", Label: "乙"}},
	}
	rawScene, err := json.Marshal(scene)
	if err != nil {
		t.Fatal(err)
	}
	schema, ok := studentinteraction.AnswerSchema(scene)
	if !ok {
		t.Fatal("interaction schema was not built")
	}
	asset := validAsset()
	asset.QuestionType = "STRUCTURED_INTERACTION"
	asset.Scene = rawScene
	asset.InputSchema = schema
	if validation := (Validator{}).Validate(asset); !validation.Passed {
		t.Fatalf("valid structured interaction rejected: %+v", validation.Checks)
	}
	asset.Scene = json.RawMessage(`{"version":"student-interaction-v2","renderer":"SINGLE_CHOICE","accessible_fallback":"文字","options":[{"id":"a","label":"甲"},{"id":"b","label":"乙"}]}`)
	if validation := (Validator{}).Validate(asset); validation.Passed {
		t.Fatal("unknown interaction version passed validation")
	}
}

type fakeReviewer struct{}

func (fakeReviewer) Review(context.Context, Asset) (Review, ReviewEvidence, error) {
	return Review{Result: ReviewPass}, ReviewEvidence{Provider: "openai", Model: "reviewer", RequestID: "review-1"}, nil
}
