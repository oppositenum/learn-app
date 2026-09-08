package tutoraudit

import (
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
)

type DeterministicResult struct {
	Passed     bool
	ReasonCode string
}

func (result DeterministicResult) Result() string {
	if result.Passed {
		return "PASS"
	}
	return "REJECT"
}

type DeterministicChecker struct{}

func (DeterministicChecker) Check(request ai.TutorOutputAuditRequest) DeterministicResult {
	protected := protectedAnswers(request)
	if len(protected) == 0 {
		return DeterministicResult{ReasonCode: "PRIVATE_ANSWER_UNAVAILABLE"}
	}
	candidate := request.Candidate.Message
	for _, segment := range request.Candidate.Segments {
		candidate += "\n" + segment.Text
	}
	for _, answer := range protected {
		if containsAnswer(candidate, answer) {
			return DeterministicResult{ReasonCode: "DETERMINISTIC_ANSWER_MATCH"}
		}
	}
	return DeterministicResult{Passed: true, ReasonCode: "NONE"}
}

func protectedAnswers(request ai.TutorOutputAuditRequest) []string {
	answers := make([]string, 0, 2)
	var decoded any
	if err := json.Unmarshal(request.PrivateAnswer.CorrectAnswer, &decoded); err == nil {
		collectAnswerValues(decoded, &answers)
	}
	if value := strings.TrimSpace(request.PrivateAnswer.TeacherReferenceAnswer); value != "" {
		answers = append(answers, value)
	}
	return uniqueNonEmpty(answers)
}

func collectAnswerValues(value any, answers *[]string) {
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range []string{"value", "answer", "answers", "correct"} {
			if nested, ok := typed[key]; ok {
				collectAnswerValues(nested, answers)
			}
		}
	case []any:
		for _, nested := range typed {
			collectAnswerValues(nested, answers)
		}
	case string:
		*answers = append(*answers, typed)
	case float64, bool:
		encoded, _ := json.Marshal(typed)
		*answers = append(*answers, string(encoded))
	}
}

func uniqueNonEmpty(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func containsAnswer(candidate, answer string) bool {
	if hasNonASCII(answer) {
		return strings.Contains(canonicalLettersAndNumbers(candidate), canonicalLettersAndNumbers(answer))
	}
	candidateTokens := asciiTokens(candidate)
	answerTokens := asciiTokens(answer)
	if len(answerTokens) == 0 || len(answerTokens) > len(candidateTokens) {
		return false
	}
	for start := 0; start+len(answerTokens) <= len(candidateTokens); start++ {
		matched := true
		for offset := range answerTokens {
			if candidateTokens[start+offset] != answerTokens[offset] {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func hasNonASCII(value string) bool {
	for _, current := range value {
		if current > utf8.RuneSelf {
			return true
		}
	}
	return false
}

func canonicalLettersAndNumbers(value string) string {
	return strings.Map(func(current rune) rune {
		if unicode.IsLetter(current) || unicode.IsNumber(current) {
			return unicode.ToLower(current)
		}
		return -1
	}, value)
}

func asciiTokens(value string) []string {
	return strings.FieldsFunc(strings.ToLower(value), func(current rune) bool {
		return !((current >= 'a' && current <= 'z') || (current >= '0' && current <= '9'))
	})
}
