package realtime

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
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

func TestParentSuppressedEventOnlyReachesStudent(t *testing.T) {
	hub := NewHub()
	studentEvents, stopStudent := hub.Subscribe("stu_1", auth.RoleStudent)
	defer stopStudent()
	parentEvents, stopParent := hub.Subscribe("stu_1", auth.RoleParent)
	defer stopParent()
	event := Event{
		EventID: "evt_2", StudentID: "stu_1", SessionID: "ses_1", Sequence: 5,
		Type: EventSafetyIntervention, CreatedAt: time.Now(), ParentSuppressed: true,
		StudentPayload: json.RawMessage(`{"category":"PERSONAL_INFORMATION"}`),
		ParentPayload:  json.RawMessage(`{}`),
	}
	if err := hub.Publish(event); err != nil {
		t.Fatal(err)
	}
	select {
	case <-studentEvents:
	case <-time.After(time.Second):
		t.Fatal("student did not receive safety event")
	}
	select {
	case payload := <-parentEvents:
		t.Fatalf("parent received suppressed event: %s", payload)
	case <-time.After(20 * time.Millisecond):
	}
}
