package content

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestStudentQuestionPayloadCarriesNoTeachingContext(t *testing.T) {
	// TeachingContext exists as a sibling of QuestionPublic precisely because
	// QuestionPublic is serialized into the student response. If a future change
	// moves those fields inside it, the student client starts receiving teaching
	// metadata, so assert the shape rather than trusting the comment.
	encoded, err := json.Marshal(QuestionPublic{
		ID: uuid.New(), KnowledgePointID: uuid.New(), Prompt: "公开题面",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{
		"subject_code", "subject_name", "knowledge_point_code",
		"knowledge_point_name", "teaching_context",
	} {
		if strings.Contains(string(encoded), leaked) {
			t.Fatalf("student question payload exposes %q: %s", leaked, encoded)
		}
	}

	// The teaching struct itself must stay serializable for the server-to-model
	// request, so this is a shape assertion, not a ban on the type.
	teaching, err := json.Marshal(TeachingContext{
		SubjectCode: "ENGLISH", SubjectName: "英语",
		KnowledgePointCode: "ENG-READ-DETAIL", KnowledgePointName: "英语阅读细节",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"subject_code", "英语阅读细节"} {
		if !strings.Contains(string(teaching), required) {
			t.Fatalf("teaching context lost %q: %s", required, teaching)
		}
	}
}

func TestTeachingContextIsIncompleteUntilEveryFieldIsPresent(t *testing.T) {
	complete := TeachingContext{
		SubjectCode: "MATH", SubjectName: "数学",
		KnowledgePointCode: "MATH-JUN-LINEAR-EQUATION", KnowledgePointName: "一元一次方程",
	}
	if !complete.Complete() {
		t.Fatal("a fully populated teaching context was rejected")
	}
	for name, mutate := range map[string]func(*TeachingContext){
		"subject code":         func(c *TeachingContext) { c.SubjectCode = "" },
		"subject name":         func(c *TeachingContext) { c.SubjectName = " " },
		"knowledge point code": func(c *TeachingContext) { c.KnowledgePointCode = "" },
		"knowledge point name": func(c *TeachingContext) { c.KnowledgePointName = "\t" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := complete
			mutate(&candidate)
			if candidate.Complete() {
				t.Fatalf("a context missing the %s was accepted", name)
			}
		})
	}
}
