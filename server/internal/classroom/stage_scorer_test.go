package classroom

import (
	"encoding/json"
	"testing"

	"github.com/oppositenum/ai-learning-tutor/server/internal/subject"
)

func TestStageScorerIsDeterministicAndSubjectAgnostic(t *testing.T) {
	for _, code := range subject.All {
		task := stageTask{SubjectCode: code, Stage: StageAbstract, ScoringRuleVersion: stageScoringExactOptionSetV1,
			ScoringRule: json.RawMessage(`{"rule_type":"EXACT_OPTION_SET","expected_option_ids":["a","c"],"allowed_option_ids":["a","b","c"]}`)}
		if got := scoreStageTask(task, json.RawMessage(`{"selected_option_ids":["c","a"]}`)); got != StageScoreCorrect {
			t.Fatalf("subject=%s score=%s", code, got)
		}
		if got := scoreStageTask(task, json.RawMessage(`{"selected_option_ids":["b"]}`)); got != StageScoreIncorrect {
			t.Fatalf("subject=%s incorrect score=%s", code, got)
		}
	}
}

func TestStageScorerFailsClosed(t *testing.T) {
	task := stageTask{SubjectCode: subject.Math, Stage: StageVariant, ScoringRuleVersion: stageScoringExactOptionSetV1,
		ScoringRule: json.RawMessage(`{"rule_type":"EXACT_OPTION_SET","expected_option_ids":["a"],"allowed_option_ids":["a","b"]}`)}
	for _, response := range []json.RawMessage{
		json.RawMessage(`{"selected_option_ids":`),
		json.RawMessage(`{"selected_option_ids":["unknown"]}`),
		json.RawMessage(`{"selected_option_ids":["a","a"]}`),
		json.RawMessage(`{"selected_option_ids":["a"],"model_correct":true}`),
	} {
		if got := scoreStageTask(task, response); got != StageScoreIndeterminate {
			t.Fatalf("response=%s score=%s", response, got)
		}
	}
}
