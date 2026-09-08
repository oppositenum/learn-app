package classroompilot

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/oppositenum/ai-learning-tutor/server/internal/subject"
)

func TestExactOptionSetScorerIsSubjectAgnostic(t *testing.T) {
	for _, subjectCode := range subject.All {
		t.Run(string(subjectCode), func(t *testing.T) {
			task := scoringTask(subjectCode)
			if got := Score(task, json.RawMessage(`{"selected_option_ids":["choice-c","choice-a"]}`)); got != ResultCorrect {
				t.Fatalf("Score(%s)=%s, want %s", subjectCode, got, ResultCorrect)
			}
			if got := Score(task, json.RawMessage(`{"selected_option_ids":["choice-b"]}`)); got != ResultIncorrect {
				t.Fatalf("Score(%s)=%s, want %s", subjectCode, got, ResultIncorrect)
			}
		})
	}
}

func TestExactOptionSetScorerFailsClosed(t *testing.T) {
	valid := scoringTask(subject.Chinese)
	tests := []struct {
		name     string
		mutate   func(Task) Task
		response json.RawMessage
	}{
		{name: "unsupported rule", mutate: func(task Task) Task {
			task.ScoringRuleVersion = "model-judged-v1"
			return task
		}, response: json.RawMessage(`{"selected_option_ids":["choice-a","choice-c"]}`)},
		{name: "malformed response", mutate: identityTask, response: json.RawMessage(`{"selected_option_ids":`)},
		{name: "unknown option", mutate: identityTask, response: json.RawMessage(`{"selected_option_ids":["choice-unknown"]}`)},
		{name: "duplicate option", mutate: identityTask, response: json.RawMessage(`{"selected_option_ids":["choice-a","choice-a"]}`)},
		{name: "unknown response field", mutate: identityTask, response: json.RawMessage(`{"selected_option_ids":["choice-a","choice-c"],"model_correct":true}`)},
		{name: "malformed rule", mutate: func(task Task) Task {
			task.ScoringRule = json.RawMessage(`{"rule_type":"EXACT_OPTION_SET","expected_option_ids":["missing"],"allowed_option_ids":["choice-a"]}`)
			return task
		}, response: json.RawMessage(`{"selected_option_ids":["choice-a"]}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Score(test.mutate(valid), test.response); got != ResultIndeterminate {
				t.Fatalf("Score=%s, want %s", got, ResultIndeterminate)
			}
		})
	}
}

func TestFourStageTransitionGraphAllowsOnlyNextStage(t *testing.T) {
	allowed := map[Stage]Stage{
		StageOriginal: StageVariant,
		StageVariant:  StageAbstract,
		StageAbstract: StageVerify,
		StageVerify:   StageComplete,
	}
	all := []Stage{StageOriginal, StageVariant, StageAbstract, StageVerify, StageComplete}
	for _, from := range all {
		for _, to := range all {
			want := allowed[from] == to
			if got := CanAdvance(from, to); got != want {
				t.Fatalf("CanAdvance(%s,%s)=%v, want %v", from, to, got, want)
			}
		}
	}
}

func TestIdempotencyDigestDoesNotFingerprintStudentResponse(t *testing.T) {
	request := SubmitRequest{
		SessionID: uuid.New(), OperationID: uuid.New(), Stage: StageVariant,
		TaskID: uuid.New(), TaskVersion: "v1", Kind: AttemptAnswer,
		Response: json.RawMessage(`{"selected_option_ids":["choice-a"]}`),
	}
	otherResponse := request
	otherResponse.Response = json.RawMessage(`{"selected_option_ids":["choice-b"]}`)
	if requestDigest(request) != requestDigest(otherResponse) {
		t.Fatal("idempotency digest included the student response")
	}
	otherTask := request
	otherTask.TaskID = uuid.New()
	if requestDigest(request) == requestDigest(otherTask) {
		t.Fatal("idempotency digest did not bind the operation to its task")
	}
}

func scoringTask(subjectCode subject.Code) Task {
	return Task{
		ID: uuid.New(), SubjectCode: subjectCode, Stage: StageAbstract,
		ScoringRuleVersion: ScoringRuleExactOptionSetV1,
		ScoringRule: json.RawMessage(`{
			"rule_type":"EXACT_OPTION_SET",
			"expected_option_ids":["choice-a","choice-c"],
			"allowed_option_ids":["choice-a","choice-b","choice-c"]
		}`),
	}
}

func identityTask(task Task) Task {
	return task
}
