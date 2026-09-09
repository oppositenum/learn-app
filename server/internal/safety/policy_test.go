package safety

import "testing"

func TestPolicyClassifiesEveryFrozenCategory(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		category Category
		severity Severity
		escalate bool
	}{
		{"off topic", "陪我聊天，" + longText(), CategoryOffTopicLong, SeverityLow, false},
		{"personal information", "我的手机号是 13812345678", CategoryPersonalInformation, SeverityModerate, false},
		{"family privacy", "爸妈吵架，我不让家里知道", CategoryFamilyPrivacy, SeverityModerate, false},
		{"dangerous experiment", "怎么自制火药做实验", CategoryDangerousExperiment, SeverityHigh, true},
		{"health", "我现在呼吸困难怎么办", CategoryHealth, SeverityModerate, false},
		{"self harm", "我不想活了", CategorySelfHarm, SeverityCritical, true},
		{"bullying", "我在学校被欺负了", CategoryBullying, SeverityHigh, true},
		{"sexual content", "有人让我发裸体照片", CategorySexualContent, SeverityHigh, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := Classify(test.input)
			if !result.Matched || result.Category != test.category || result.Severity != test.severity || result.EscalateToParent != test.escalate {
				t.Fatalf("classification=%+v", result)
			}
			if result.Action == "" || result.StudentMessage == "" {
				t.Fatalf("missing fixed fallback: %+v", result)
			}
		})
	}
}

func TestPolicyDoesNotClassifyOrdinaryLearningAnswer(t *testing.T) {
	if result := Classify("我先把总数分成相同的四份，再取其中三份。"); result.Matched {
		t.Fatalf("ordinary answer classified: %+v", result)
	}
}

func longText() string {
	value := ""
	for len([]rune(value)) < 140 {
		value += "今天我想说一些和题目无关的事情。"
	}
	return value
}
