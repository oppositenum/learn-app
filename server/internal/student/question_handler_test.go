package student

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/oppositenum/ai-learning-tutor/server/internal/content"
	"github.com/oppositenum/ai-learning-tutor/server/internal/curriculum"
)

var forbiddenStudentKeys = map[string]struct{}{
	"correct_answer":           {},
	"answer_correct":           {},
	"full_solution":            {},
	"teacher_reference_answer": {},
	"teacher_solution":         {},
	"scoring_key":              {},
	"misconceptions":           {},
	"hint_policy":              {},
}

type publicQuestionStub struct {
	question content.QuestionPublic
	err      error
}

func (stub publicQuestionStub) ReleasedPublicQuestion(context.Context, uuid.UUID) (content.QuestionPublic, error) {
	return stub.question, stub.err
}

func TestStudentQuestionResponseDoesNotDisclosePrivateAnswer(t *testing.T) {
	questionID := uuid.New()
	handler := NewQuestionHandler(publicQuestionStub{question: content.QuestionPublic{
		ID:               questionID,
		KnowledgePointID: uuid.New(),
		Difficulty:       curriculum.DifficultyL2,
		QuestionType:     "FREE_TEXT",
		Prompt:           "3杯同价饮料加6元配送费共36元，每杯多少钱？",
		Scene:            json.RawMessage(`{"kind":"SHOPPING"}`),
		InputSchema:      json.RawMessage(`{"type":"string"}`),
		ContentVersion:   "v1",
	}})

	request := httptest.NewRequest(http.MethodGet, "/api/v1/student/questions/"+questionID.String(), nil)
	request.SetPathValue("id", questionID.String())
	response := httptest.NewRecorder()
	handler.Get(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, response.Code, response.Body.String())
	}

	var payload any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	assertNoForbiddenKeys(t, payload, "response")

}

func TestStudentResponseTypeCannotContainPrivateAnswer(t *testing.T) {
	privateType := reflect.TypeOf(content.QuestionPrivateAnswer{})
	assertTypeDoesNotReach(t, reflect.TypeOf(questionResponse{}), privateType, map[reflect.Type]bool{})
}

func assertNoForbiddenKeys(t *testing.T, value any, path string) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if _, forbidden := forbiddenStudentKeys[strings.ToLower(key)]; forbidden {
				t.Fatalf("student payload contains forbidden key %s.%s", path, key)
			}
			assertNoForbiddenKeys(t, child, path+"."+key)
		}
	case []any:
		for _, child := range typed {
			assertNoForbiddenKeys(t, child, path+"[]")
		}
	}
}

func assertTypeDoesNotReach(t *testing.T, current, forbidden reflect.Type, visited map[reflect.Type]bool) {
	t.Helper()
	if current == forbidden {
		t.Fatalf("student response type reaches private answer type %s", forbidden)
	}
	if visited[current] {
		return
	}
	visited[current] = true

	switch current.Kind() {
	case reflect.Pointer, reflect.Array, reflect.Slice:
		assertTypeDoesNotReach(t, current.Elem(), forbidden, visited)
	case reflect.Map:
		assertTypeDoesNotReach(t, current.Key(), forbidden, visited)
		assertTypeDoesNotReach(t, current.Elem(), forbidden, visited)
	case reflect.Struct:
		for index := 0; index < current.NumField(); index++ {
			assertTypeDoesNotReach(t, current.Field(index).Type, forbidden, visited)
		}
	}
}
