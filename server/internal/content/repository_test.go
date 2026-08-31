package content

import (
	"strings"
	"testing"
)

func TestPublicQuestionQueryCannotReadPrivateAnswerTable(t *testing.T) {
	query := strings.ToLower(releasedPublicQuestionQuery)
	for _, forbidden := range []string{
		"question_private_answers",
		"correct_answer",
		"full_solution",
		"teacher_reference_answer",
		"scoring_key",
		"misconceptions_private",
		"hint_policy_private",
	} {
		if strings.Contains(query, forbidden) {
			t.Fatalf("public question query contains private identifier %q", forbidden)
		}
	}
}

func TestRuntimeQueriesOnlyReadReleasedContent(t *testing.T) {
	for name, query := range map[string]string{
		"public":   releasedPublicQuestionQuery,
		"teaching": releasedTeachingQuestionQuery,
	} {
		if !strings.Contains(query, "status = 'RELEASED'") && !strings.Contains(query, "status = 'released'") {
			t.Fatalf("%s runtime query does not enforce RELEASED content", name)
		}
	}
}
