package realtime

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestStudentAndParentDTOsAreSeparated(t *testing.T) {
	event := Event{
		EventID: "evt_1", StudentID: "stu_1", SessionID: "ses_1", Sequence: 4,
		Type: EventAnswerAnalyzed, CreatedAt: time.Now(),
		StudentPayload: json.RawMessage(`{"message":"我们再看一步。"}`),
		ParentPayload:  json.RawMessage(`{"correct_answer":"10","error_type":"FIXED_COST_IGNORED"}`),
	}

	student, err := json.Marshal(ProjectStudent(event))
	if err != nil {
		t.Fatal(err)
	}
	parent, err := json.Marshal(ProjectParent(event))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(student), "correct_answer") || strings.Contains(string(student), "FIXED_COST_IGNORED") {
		t.Fatalf("student DTO leaked parent data: %s", student)
	}
	if !strings.Contains(string(parent), "correct_answer") {
		t.Fatalf("parent DTO lost supervised answer data: %s", parent)
	}
}
