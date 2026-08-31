package mastery

import (
	"testing"
	"time"
)

func TestOneCorrectAnswerNeverMasters(t *testing.T) {
	now := time.Now()
	engine := NewEngine()
	got := engine.Apply(Skill{}, Evidence{Correct: true, Independent: true, Form: FormLife, At: now})
	if got.State == Mastered || got.State == Understood {
		t.Fatalf("one correct answer advanced too far: %+v", got)
	}
	if score := engine.Score(got); score != 25 {
		t.Fatalf("one-form score=%d, want 25", score)
	}
}

func TestMasteryRequiresAllFormsAndDelayedReview(t *testing.T) {
	engine, now := NewEngine(), time.Now()
	skill := Skill{}
	for _, form := range []Form{FormLife, FormVariant, FormTextbook} {
		skill = engine.Apply(skill, Evidence{Correct: true, Independent: true, Form: form, At: now})
	}
	if skill.State != Understood {
		t.Fatalf("pre-review state = %s", skill.State)
	}
	skill = engine.Apply(skill, Evidence{Correct: true, Independent: true, Form: FormReview, At: now.Add(24 * time.Hour)})
	if skill.State != Mastered || skill.ReviewSuccesses != 1 {
		t.Fatalf("mastery evidence = %+v", skill)
	}
	if score := engine.Score(skill); score != 100 {
		t.Fatalf("mastery score=%d, want 100", score)
	}
}

func TestFailedReviewRegressesAndReschedules(t *testing.T) {
	now := time.Now()
	got := NewEngine().Apply(Skill{State: Mastered}, Evidence{Correct: false, Form: FormReview, At: now})
	if got.State != Regressed || got.NextReviewAt == nil || !got.NextReviewAt.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("failed review result = %+v", got)
	}
	if score := NewEngine().Score(got); score != 0 {
		t.Fatalf("empty regressed score=%d, want 0", score)
	}
}

func TestAssistedSuccessesCannotSatisfyIndependentMasteryForms(t *testing.T) {
	engine, now := NewEngine(), time.Now()
	skill := Skill{}
	for _, form := range []Form{FormLife, FormVariant, FormTextbook, FormReview} {
		skill = engine.Apply(skill, Evidence{Correct: true, Independent: false, Form: form, At: now})
	}
	if skill.State != Assisted || skill.AssistedSuccesses != 4 || skill.LifeContextSuccesses != 0 || skill.VariantSuccesses != 0 || skill.TextbookSuccesses != 0 || skill.ReviewSuccesses != 0 {
		t.Fatalf("assisted evidence incorrectly counted as independent: %+v", skill)
	}
	if score := engine.Score(skill); score != 10 {
		t.Fatalf("assisted score=%d, want 10", score)
	}
}
