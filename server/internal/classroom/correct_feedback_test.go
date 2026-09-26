package classroom

import (
	"strings"
	"testing"
)

func TestCorrectAnswerMessageNamesWhatTheChildDid(t *testing.T) {
	const mastered = "你已经在生活、变式、课本和跨天复习中都能独立解决，掌握证据完整。"
	cases := []struct {
		name                          string
		explained, corrected, mastery bool
		want                          string
	}{
		{name: "first try", want: "你自己把这道题做对了。"},
		{name: "after a wrong answer or help", corrected: true, want: "你把刚才没做对的地方改对了。"},
		{name: "explained reasoning", explained: true, want: "你把做法说清楚了，结果也对。"},
		{name: "explained after a wrong answer", explained: true, corrected: true, want: "你把做法说清楚了，结果也对。"},
		{name: "first try with mastery", mastery: true, want: "你自己把这道题做对了。" + mastered},
		{name: "corrected with mastery", corrected: true, mastery: true, want: "你把刚才没做对的地方改对了。" + mastered},
	}
	for _, testCase := range cases {
		got := correctAnswerMessage(testCase.explained, testCase.corrected, testCase.mastery)
		if got != testCase.want {
			t.Fatalf("%s: message=%q want %q", testCase.name, got, testCase.want)
		}
		if !testCase.explained && strings.Contains(got, "说清楚了") {
			t.Fatalf("%s: message claims the reasoning was explained", testCase.name)
		}
	}
}
