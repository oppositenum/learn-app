package classroom

import (
	"testing"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
)

func TestLegacyAnswerEvaluationProvenanceSeparatesInputsFromResolution(t *testing.T) {
	modelAccepted := ai.AnalyzeAnswerResult{AnswerCorrect: true, Confidence: 0.95}
	modelRejected := ai.AnalyzeAnswerResult{AnswerCorrect: false, Confidence: 0.97}
	for _, test := range []struct {
		name                 string
		deterministicCorrect bool
		analysis             *ai.AnalyzeAnswerResult
		finalCorrect         bool
		wantDeterministic    string
		wantResolution       string
		wantModel            *bool
	}{
		{
			name: "deterministic match without model", deterministicCorrect: true, finalCorrect: true,
			wantDeterministic: "MATCH", wantResolution: "DETERMINISTIC_ACCEPTED",
		},
		{
			name: "model mediated legacy acceptance", analysis: &modelAccepted, finalCorrect: true,
			wantDeterministic: "NO_MATCH", wantResolution: "MODEL_MEDIATED_ACCEPTED", wantModel: boolPointer(true),
		},
		{
			name: "legacy rejection", analysis: &modelRejected,
			wantDeterministic: "NO_MATCH", wantResolution: "NOT_ACCEPTED", wantModel: boolPointer(false),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := legacyAnswerEvaluationProvenance(test.deterministicCorrect, test.analysis, test.finalCorrect)
			if got.deterministicResult != test.wantDeterministic || got.legacyResolution != test.wantResolution || got.finalCorrect != test.finalCorrect {
				t.Fatalf("provenance result=%s resolution=%s final=%t", got.deterministicResult, got.legacyResolution, got.finalCorrect)
			}
			if test.wantModel == nil {
				if got.modelAnswerCorrect != nil || got.modelConfidence != nil || got.semanticVersion != nil {
					t.Fatal("model-free evaluation stored model provenance")
				}
				return
			}
			if got.modelAnswerCorrect == nil || *got.modelAnswerCorrect != *test.wantModel ||
				got.modelConfidence == nil || *got.modelConfidence != test.analysis.Confidence ||
				got.semanticVersion == nil || *got.semanticVersion != semanticPolicyVersion {
				t.Fatalf("raw model provenance=%v/%v/%v", got.modelAnswerCorrect, got.modelConfidence, got.semanticVersion)
			}
		})
	}
}

func boolPointer(value bool) *bool { return &value }
