package classroom

import "testing"

func TestAnswersMatchAcceptsStemAlignedTicketRepresentations(t *testing.T) {
	scoringKey := []byte(`{"accepted_answers":["设每张x元","设每张门票的价格为x元","(36-6)/3","(36－6)÷3","10"]}`)
	reference := "设每张x元，3x+6=36"
	for _, answer := range []string{
		"设每张x元，3x+6=36",
		"设每张门票的价格为 x 元",
		"(36－6)÷3",
		"(36-6)/3",
		"10",
	} {
		if !answersMatch(answer, reference, scoringKey) {
			t.Fatalf("stem-aligned answer %q should MATCH", answer)
		}
	}
	if answersMatch("42", reference, scoringKey) {
		t.Fatal("unrelated number matched the ticket stem")
	}
}

func TestIsHelpRequestRecognizesUnknownWords(t *testing.T) {
	for _, answer := range []string{"我看不懂这个英文", "leftovers 是什么意思", "I don't know this word", "不会"} {
		if !isHelpRequest(answer) {
			t.Fatalf("help request %q was treated as a solution", answer)
		}
	}
}

func TestIsHelpRequestDoesNotMatchMarkerSubstringsInOrdinaryAnswers(t *testing.T) {
	for _, answer := range []string{"不会被保存的回答", "铁钉生锈不会生成新物质", "请提示我下一步之前先看题", "helpful note"} {
		if isHelpRequest(answer) {
			t.Fatalf("ordinary answer %q was classified as help", answer)
		}
	}
}

func TestEvaluateSubmittedAnswerMatchesBeforeHelpIntent(t *testing.T) {
	scoringKey := []byte(`{"accepted_answers":["铁钉生锈，因为生成了新物质"]}`)
	reference := "铁钉生锈，因为生成了新物质"
	matched := evaluateSubmittedAnswer(reference, reference, scoringKey, nil)
	if !matched.deterministicCorrect || !matched.correct {
		t.Fatalf("matching answer without an agent was not accepted: %+v", matched)
	}
	helpInsideWrong := evaluateSubmittedAnswer("不会被保存的回答", reference, scoringKey, nil)
	if helpInsideWrong.deterministicCorrect || helpInsideWrong.correct {
		t.Fatalf("unrelated answer with 不会 was accepted: %+v", helpInsideWrong)
	}
	if unmatchedHelpRequest("不会被保存的回答", reference, scoringKey) {
		t.Fatal("ordinary Chinese containing 不会 was treated as a help request")
	}
	if !unmatchedHelpRequest("我看不懂", reference, scoringKey) {
		t.Fatal("unmatched help request was not recognized")
	}
	if unmatchedHelpRequest(reference, reference, scoringKey) {
		t.Fatal("matching answer was classified as help")
	}
	matchedHelpPhrase := evaluateSubmittedAnswer("不会", "不会", nil, nil)
	if !matchedHelpPhrase.deterministicCorrect || !matchedHelpPhrase.correct {
		t.Fatalf("matching 不会 without an agent was not accepted: %+v", matchedHelpPhrase)
	}
	if unmatchedHelpRequest("不会", "不会", nil) {
		t.Fatal("matching 不会 was classified as unmatched help")
	}
	withAgent := evaluateSubmittedAnswer(reference, reference, scoringKey, &preparedAgent{deterministicMatch: true})
	withoutAgent := evaluateSubmittedAnswer(reference, reference, scoringKey, nil)
	if withAgent != withoutAgent {
		t.Fatalf("MATCH scoring diverged with/without agent: %+v vs %+v", withAgent, withoutAgent)
	}
}
