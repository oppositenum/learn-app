package planner

import (
	"testing"
	"time"
)

func TestBuildPrioritizesCrossSubjectAndReviewEvidence(t *testing.T) {
	now := time.Date(2026, 8, 26, 9, 0, 0, 0, time.UTC)
	due := now.Add(-time.Hour)
	plan := (Engine{}).Build(Input{Date: now, Preferences: Preferences{DailyMinutes: 30}, Candidates: []Candidate{
		juniorCandidate(Candidate{SubjectCode: "PHYSICS", KnowledgePointID: "SPEED", SkillScore: 55}),
		primaryPrerequisite(Candidate{SubjectCode: "MATH", KnowledgePointID: "UNIT-CONVERSION", SkillScore: 30, CrossSubjectGap: true, OriginalTaskID: "physics-task"}),
		juniorCandidate(Candidate{SubjectCode: "ENGLISH", KnowledgePointID: "THERE-IS", SkillScore: 60, ReviewDueAt: &due}),
	}})
	if len(plan.Blocks) != 3 || plan.Blocks[0].Mode != ModeMicroBacktrack || plan.Blocks[0].OriginalTaskID != "physics-task" {
		t.Fatalf("cross-subject remediation did not preserve original task: %+v", plan)
	}
	if plan.Blocks[2].Minutes != 10 || plan.TargetMinutes != 30 {
		t.Fatalf("time budget was not allocated deterministically: %+v", plan)
	}
}

func TestBuildReviewOnlyExcludesNewContent(t *testing.T) {
	now := time.Now()
	plan := (Engine{}).Build(Input{Date: now, Preferences: Preferences{DailyMinutes: 20, ReviewOnly: true}, Candidates: []Candidate{
		juniorCandidate(Candidate{SubjectCode: "MATH", KnowledgePointID: "NEW"}),
		juniorCandidate(Candidate{SubjectCode: "CHINESE", KnowledgePointID: "EVIDENCE", ActiveMisconception: true}),
	}})
	if len(plan.Blocks) != 1 || plan.Blocks[0].KnowledgePointID != "EVIDENCE" || plan.Blocks[0].Mode != ModeRemediation {
		t.Fatalf("review-only plan included new content: %+v", plan)
	}
}

func TestBuildPrefersFoundationWeakPointsOverLaterCatalog(t *testing.T) {
	now := time.Date(2026, 9, 22, 9, 0, 0, 0, time.UTC)
	plan := (Engine{}).Build(Input{Date: now, Preferences: Preferences{DailyMinutes: 30}, Candidates: []Candidate{
		juniorCandidate(Candidate{SubjectCode: "ENGLISH", KnowledgePointID: "later-reading", FoundationPriority: foundationPriority("ENGLISH", "ENGLISH-JUN-READING-COMPREHENSION")}),
		juniorCandidate(Candidate{SubjectCode: "ENGLISH", KnowledgePointID: "vocab", FoundationPriority: foundationPriority("ENGLISH", "ENGLISH-JUN-VOCABULARY")}),
		juniorCandidate(Candidate{SubjectCode: "MATH", KnowledgePointID: "later-systems", FoundationPriority: foundationPriority("MATH", "MATH-JUN-LINEAR-SYSTEMS")}),
		juniorCandidate(Candidate{SubjectCode: "MATH", KnowledgePointID: "linear", FoundationPriority: foundationPriority("MATH", "MATH-LINEAR-EQUATION")}),
	}})
	if len(plan.Blocks) != 2 {
		t.Fatalf("expected one English and one Math block, got %+v", plan.Blocks)
	}
	bySubject := map[string]string{}
	for _, block := range plan.Blocks {
		bySubject[block.SubjectCode] = block.KnowledgePointID
	}
	if bySubject["ENGLISH"] != "vocab" {
		t.Fatalf("english block=%s want vocab: %+v", bySubject["ENGLISH"], plan.Blocks)
	}
	if bySubject["MATH"] != "linear" {
		t.Fatalf("math block=%s want linear: %+v", bySubject["MATH"], plan.Blocks)
	}
}

func TestBuildEmitsOneBlockPerSubject(t *testing.T) {
	now := time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC)
	plan := (Engine{}).Build(Input{Date: now, Preferences: Preferences{DailyMinutes: 30}, Candidates: []Candidate{
		juniorCandidate(Candidate{SubjectCode: "MATH", KnowledgePointID: "M1"}),
		juniorCandidate(Candidate{SubjectCode: "MATH", KnowledgePointID: "M2"}),
		juniorCandidate(Candidate{SubjectCode: "CHINESE", KnowledgePointID: "C1"}),
		juniorCandidate(Candidate{SubjectCode: "ENGLISH", KnowledgePointID: "E1"}),
		juniorCandidate(Candidate{SubjectCode: "PHYSICS", KnowledgePointID: "P1"}),
		juniorCandidate(Candidate{SubjectCode: "CHEMISTRY", KnowledgePointID: "CH1"}),
	}})
	if len(plan.Blocks) != 5 {
		t.Fatalf("expected one block per subject, got %d: %+v", len(plan.Blocks), plan)
	}
	subjects := map[string]int{}
	total := 0
	for _, block := range plan.Blocks {
		subjects[block.SubjectCode]++
		total += block.Minutes
	}
	for subject, count := range subjects {
		if count != 1 {
			t.Fatalf("subject %s appeared %d times: %+v", subject, count, plan)
		}
	}
	if total != 30 {
		t.Fatalf("block minutes should sum to target, got %d: %+v", total, plan)
	}
}

func TestBuildSingleSubjectGetsFullBudget(t *testing.T) {
	now := time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC)
	plan := (Engine{}).Build(Input{Date: now, Preferences: Preferences{DailyMinutes: 30}, Candidates: []Candidate{
		juniorCandidate(Candidate{SubjectCode: "MATH", KnowledgePointID: "M1", SkillScore: 10}),
		juniorCandidate(Candidate{SubjectCode: "MATH", KnowledgePointID: "M2", SkillScore: 20}),
	}})
	if len(plan.Blocks) != 1 || plan.Blocks[0].SubjectCode != "MATH" || plan.Blocks[0].Minutes != 30 {
		t.Fatalf("single enabled subject should own the full budget: %+v", plan)
	}
	if plan.Blocks[0].KnowledgePointID != "M1" {
		t.Fatalf("should pick the highest-ranked (lowest score) candidate: %+v", plan)
	}
}

func TestGradeBandAllowsModeUsesAsymmetricBoundaries(t *testing.T) {
	tests := []struct {
		name                   string
		studentGrade, min, max int
		mode                   Mode
		want                   bool
	}{
		{name: "grade one current primary", studentGrade: 1, min: 1, max: 6, mode: ModeCurrentGrade, want: true},
		{name: "grade six current primary", studentGrade: 6, min: 1, max: 6, mode: ModeCurrentGrade, want: true},
		{name: "grade seven current junior", studentGrade: 7, min: 7, max: 9, mode: ModeCurrentGrade, want: true},
		{name: "grade nine current junior", studentGrade: 9, min: 7, max: 9, mode: ModeCurrentGrade, want: true},
		{name: "current grade cannot move down", studentGrade: 7, min: 1, max: 6, mode: ModeCurrentGrade, want: false},
		{name: "remediation can move down", studentGrade: 7, min: 1, max: 6, mode: ModeRemediation, want: true},
		{name: "review can move down", studentGrade: 7, min: 1, max: 6, mode: ModeReview, want: true},
		{name: "micro backtrack can move down", studentGrade: 7, min: 1, max: 6, mode: ModeMicroBacktrack, want: true},
		{name: "no mode can move up", studentGrade: 6, min: 7, max: 9, mode: ModeRemediation, want: false},
		{name: "missing grade metadata fails closed", studentGrade: 0, min: 0, max: 0, mode: ModeReview, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := gradeBandAllowsMode(test.studentGrade, test.min, test.max, test.mode); got != test.want {
				t.Fatalf("gradeBandAllowsMode(%d,%d,%d,%s)=%v want %v", test.studentGrade, test.min, test.max, test.mode, got, test.want)
			}
		})
	}
}

func TestBuildExcludesLowerBandCurrentContentButKeepsLowerBandSupport(t *testing.T) {
	now := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	plan := (Engine{}).Build(Input{Date: now, Preferences: Preferences{DailyMinutes: 20}, Candidates: []Candidate{
		primaryPrerequisite(Candidate{SubjectCode: "MATH", KnowledgePointID: "PRIMARY-CURRENT"}),
		primaryPrerequisite(Candidate{SubjectCode: "CHINESE", KnowledgePointID: "PRIMARY-REMEDIATION", ActiveMisconception: true}),
		juniorCandidate(Candidate{SubjectCode: "MATH", KnowledgePointID: "JUNIOR-CURRENT"}),
	}})
	if len(plan.Blocks) != 2 {
		t.Fatalf("grade-aware plan blocks=%+v", plan.Blocks)
	}
	for _, block := range plan.Blocks {
		if block.KnowledgePointID == "PRIMARY-CURRENT" {
			t.Fatalf("CURRENT_GRADE moved down into a prerequisite band: %+v", plan.Blocks)
		}
	}
	if plan.Blocks[0].KnowledgePointID != "PRIMARY-REMEDIATION" || plan.Blocks[0].Mode != ModeRemediation {
		t.Fatalf("lower-band remediation was not retained: %+v", plan.Blocks)
	}
}

func juniorCandidate(candidate Candidate) Candidate {
	candidate.StudentGrade = 7
	candidate.GradeBandMin = 7
	candidate.GradeBandMax = 9
	return candidate
}

func primaryPrerequisite(candidate Candidate) Candidate {
	candidate.StudentGrade = 7
	candidate.GradeBandMin = 1
	candidate.GradeBandMax = 6
	return candidate
}

func TestReplanDeescalatesBeforeAddingWork(t *testing.T) {
	engine := Engine{}
	if got := engine.Replan(SessionSignal{EmotionLow: true, EngagementLow: true, PrerequisiteGap: true}); got != EndEarly {
		t.Fatalf("emotion deescalation = %s", got)
	}
	if got := engine.Replan(SessionSignal{PrerequisiteGap: true}); got != InsertMicroRemediation {
		t.Fatalf("prerequisite replan = %s", got)
	}
}

func TestPlanDateUsesShanghaiLearningDay(t *testing.T) {
	justAfterShanghaiMidnight := time.Date(2030, 2, 3, 16, 1, 0, 0, time.UTC)
	got := day(justAfterShanghaiMidnight)
	if got.Format("2006-01-02") != "2030-02-04" || got.Location().String() != "Asia/Shanghai" {
		t.Fatalf("learning day=%s location=%s", got.Format(time.RFC3339), got.Location())
	}
}
