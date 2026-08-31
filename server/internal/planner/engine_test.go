package planner

import (
	"testing"
	"time"
)

func TestBuildPrioritizesCrossSubjectAndReviewEvidence(t *testing.T) {
	now := time.Date(2026, 8, 26, 9, 0, 0, 0, time.UTC)
	due := now.Add(-time.Hour)
	plan := (Engine{}).Build(Input{Date: now, Preferences: Preferences{DailyMinutes: 30}, Candidates: []Candidate{
		{SubjectCode: "PHYSICS", KnowledgePointID: "SPEED", SkillScore: 55},
		{SubjectCode: "MATH", KnowledgePointID: "UNIT-CONVERSION", SkillScore: 30, CrossSubjectGap: true, OriginalTaskID: "physics-task"},
		{SubjectCode: "ENGLISH", KnowledgePointID: "THERE-IS", SkillScore: 60, ReviewDueAt: &due},
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
		{SubjectCode: "MATH", KnowledgePointID: "NEW"},
		{SubjectCode: "CHINESE", KnowledgePointID: "EVIDENCE", ActiveMisconception: true},
	}})
	if len(plan.Blocks) != 1 || plan.Blocks[0].KnowledgePointID != "EVIDENCE" || plan.Blocks[0].Mode != ModeRemediation {
		t.Fatalf("review-only plan included new content: %+v", plan)
	}
}

func TestBuildEmitsOneBlockPerSubject(t *testing.T) {
	now := time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC)
	plan := (Engine{}).Build(Input{Date: now, Preferences: Preferences{DailyMinutes: 30}, Candidates: []Candidate{
		{SubjectCode: "MATH", KnowledgePointID: "M1"},
		{SubjectCode: "MATH", KnowledgePointID: "M2"},
		{SubjectCode: "CHINESE", KnowledgePointID: "C1"},
		{SubjectCode: "ENGLISH", KnowledgePointID: "E1"},
		{SubjectCode: "PHYSICS", KnowledgePointID: "P1"},
		{SubjectCode: "CHEMISTRY", KnowledgePointID: "CH1"},
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
		{SubjectCode: "MATH", KnowledgePointID: "M1", SkillScore: 10},
		{SubjectCode: "MATH", KnowledgePointID: "M2", SkillScore: 20},
	}})
	if len(plan.Blocks) != 1 || plan.Blocks[0].SubjectCode != "MATH" || plan.Blocks[0].Minutes != 30 {
		t.Fatalf("single enabled subject should own the full budget: %+v", plan)
	}
	if plan.Blocks[0].KnowledgePointID != "M1" {
		t.Fatalf("should pick the highest-ranked (lowest score) candidate: %+v", plan)
	}
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
