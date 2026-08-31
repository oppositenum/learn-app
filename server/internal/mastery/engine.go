package mastery

import "time"

type State string

const (
	Unknown    State = "UNKNOWN"
	Exposed    State = "EXPOSED"
	Learning   State = "LEARNING"
	Assisted   State = "ASSISTED"
	Understood State = "UNDERSTOOD"
	Mastered   State = "MASTERED"
	ReviewDue  State = "REVIEW_DUE"
	Regressed  State = "REGRESSED"
)

type Form string

const (
	FormLife     Form = "LIFE"
	FormVariant  Form = "VARIANT"
	FormTextbook Form = "TEXTBOOK"
	FormReview   Form = "REVIEW"
)

type Evidence struct {
	Correct     bool
	Independent bool
	Form        Form
	At          time.Time
}

type Skill struct {
	State                     State
	IndependentSuccesses      int
	AssistedSuccesses         int
	LifeContextSuccesses      int
	VariantSuccesses          int
	TextbookSuccesses         int
	ReviewSuccesses           int
	ConsecutiveReviewFailures int
	NextReviewAt              *time.Time
}

type Engine struct{ Intervals []time.Duration }

func NewEngine() Engine {
	return Engine{Intervals: []time.Duration{24 * time.Hour, 3 * 24 * time.Hour, 7 * 24 * time.Hour, 14 * 24 * time.Hour}}
}

func (engine Engine) Apply(skill Skill, evidence Evidence) Skill {
	if skill.State == "" {
		skill.State = Unknown
	}
	if !evidence.Correct {
		if evidence.Form == FormReview {
			skill.ConsecutiveReviewFailures++
			skill.State = Regressed
			next := evidence.At.Add(engine.Intervals[0])
			skill.NextReviewAt = &next
		} else if skill.State == Unknown || skill.State == Exposed {
			skill.State = Learning
		}
		return skill
	}

	if evidence.Independent {
		skill.IndependentSuccesses++
	} else {
		skill.AssistedSuccesses++
	}
	if evidence.Independent {
		switch evidence.Form {
		case FormLife:
			skill.LifeContextSuccesses++
		case FormVariant:
			skill.VariantSuccesses++
		case FormTextbook:
			skill.TextbookSuccesses++
		case FormReview:
			skill.ReviewSuccesses++
			skill.ConsecutiveReviewFailures = 0
		}
	}

	forms := boolInt(skill.LifeContextSuccesses > 0) + boolInt(skill.VariantSuccesses > 0) + boolInt(skill.TextbookSuccesses > 0)
	if skill.IndependentSuccesses > 0 && forms >= 2 {
		skill.State = Understood
	} else if evidence.Independent {
		skill.State = Learning
	} else {
		skill.State = Assisted
	}
	if skill.LifeContextSuccesses > 0 && skill.VariantSuccesses > 0 && skill.TextbookSuccesses > 0 && skill.ReviewSuccesses > 0 {
		skill.State = Mastered
	}
	intervalIndex := min(skill.ReviewSuccesses, len(engine.Intervals)-1)
	next := evidence.At.Add(engine.Intervals[intervalIndex])
	skill.NextReviewAt = &next
	return skill
}

// Score derives a display/planning score from rules-engine evidence. It is not
// accepted from an AI provider or client payload.
func (engine Engine) Score(skill Skill) int {
	forms := boolInt(skill.LifeContextSuccesses > 0) + boolInt(skill.VariantSuccesses > 0) + boolInt(skill.TextbookSuccesses > 0) + boolInt(skill.ReviewSuccesses > 0)
	score := forms * 25
	if score == 0 && skill.AssistedSuccesses > 0 {
		score = 10
	}
	switch skill.State {
	case Unknown:
		return 0
	case Exposed:
		if score < 5 {
			return 5
		}
	case Regressed:
		if score > 40 {
			return 40
		}
	case Mastered:
		return 100
	}
	return score
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
