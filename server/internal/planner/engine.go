package planner

import (
	"sort"
	"time"
)

var planLocation = func() *time.Location {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("Asia/Shanghai", 8*60*60)
	}
	return location
}()

type Mode string

const (
	ModeRemediation    Mode = "REMEDIATION"
	ModeCurrentGrade   Mode = "CURRENT_GRADE"
	ModeReview         Mode = "REVIEW"
	ModeMicroBacktrack Mode = "MICRO_BACKTRACK"
)

type Candidate struct {
	SubjectCode         string
	KnowledgePointID    string
	StudentGrade        int
	GradeBandMin        int
	GradeBandMax        int
	SkillScore          float64
	ReviewDueAt         *time.Time
	ActiveMisconception bool
	ParentPriority      bool
	CrossSubjectGap     bool
	OriginalTaskID      string
}

type Preferences struct {
	DailyMinutes    int
	ReviewOnly      bool
	ReduceIntensity bool
}

type Input struct {
	Date        time.Time
	Candidates  []Candidate
	Preferences Preferences
}

type Block struct {
	Sequence         int
	SubjectCode      string
	KnowledgePointID string
	Minutes          int
	Mode             Mode
	Reason           string
	OriginalTaskID   string
}

type Plan struct {
	Date          time.Time
	TargetMinutes int
	Blocks        []Block
}

type Engine struct{}

func (Engine) Build(input Input) Plan {
	target := input.Preferences.DailyMinutes
	if target == 0 {
		target = 30
	}
	if input.Preferences.ReduceIntensity && target > 15 {
		target = max(15, target*2/3)
	}
	target = min(max(target, 15), 60)

	candidates := append([]Candidate(nil), input.Candidates...)
	now := input.Date
	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := candidateRank(candidates[i], now, input.Preferences.ReviewOnly), candidateRank(candidates[j], now, input.Preferences.ReviewOnly)
		if left != right {
			return left > right
		}
		if candidates[i].SkillScore != candidates[j].SkillScore {
			return candidates[i].SkillScore < candidates[j].SkillScore
		}
		if candidates[i].SubjectCode != candidates[j].SubjectCode {
			return candidates[i].SubjectCode < candidates[j].SubjectCode
		}
		return candidates[i].KnowledgePointID < candidates[j].KnowledgePointID
	})

	eligible := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if input.Preferences.ReviewOnly && !isReview(candidate, now) {
			continue
		}
		eligible = append(eligible, candidate)
	}
	if input.Preferences.ReviewOnly && len(eligible) == 0 {
		eligible = candidates
	}
	gradeEligible := eligible[:0]
	for _, candidate := range eligible {
		mode, _ := candidateMode(candidate, now, input.Preferences.ReviewOnly)
		if gradeBandAllowsMode(candidate.StudentGrade, candidate.GradeBandMin, candidate.GradeBandMax, mode) {
			gradeEligible = append(gradeEligible, candidate)
		}
	}
	eligible = gradeEligible
	if len(eligible) == 0 {
		return Plan{Date: day(input.Date), TargetMinutes: target}
	}

	selected := diversify(eligible)
	count := len(selected)
	base, remainder := target/count, target%count
	blocks := make([]Block, 0, count)
	for index, candidate := range selected {
		minutes := base
		if index < remainder {
			minutes++
		}
		mode, reason := candidateMode(candidate, now, input.Preferences.ReviewOnly)
		blocks = append(blocks, Block{
			Sequence: index + 1, SubjectCode: candidate.SubjectCode,
			KnowledgePointID: candidate.KnowledgePointID, Minutes: minutes,
			Mode: mode, Reason: reason, OriginalTaskID: candidate.OriginalTaskID,
		})
	}
	return Plan{Date: day(input.Date), TargetMinutes: target, Blocks: blocks}
}

func candidateMode(candidate Candidate, now time.Time, reviewOnly bool) (Mode, string) {
	mode, reason := classify(candidate, now)
	if reviewOnly && !isReview(candidate, now) {
		return ModeReview, "parent_review_only"
	}
	return mode, reason
}

func gradeBandAllowsMode(studentGrade, minGrade, maxGrade int, mode Mode) bool {
	if studentGrade < 1 || minGrade < 1 || maxGrade < minGrade || minGrade > studentGrade {
		return false
	}
	if mode == ModeCurrentGrade {
		return studentGrade <= maxGrade
	}
	return mode == ModeRemediation || mode == ModeMicroBacktrack || mode == ModeReview
}

// diversify keeps one candidate per subject — the highest-ranked one, since
// candidates arrive pre-sorted — so the plan carries one block per subject
// present ("勾几科出几科").
func diversify(candidates []Candidate) []Candidate {
	selected := make([]Candidate, 0, len(candidates))
	subjects := map[string]bool{}
	for _, candidate := range candidates {
		if subjects[candidate.SubjectCode] {
			continue
		}
		selected = append(selected, candidate)
		subjects[candidate.SubjectCode] = true
	}
	return selected
}

func candidateRank(candidate Candidate, now time.Time, reviewOnly bool) int {
	score := 0
	if candidate.CrossSubjectGap {
		score += 100
	}
	if candidate.ActiveMisconception {
		score += 80
	}
	if candidate.ReviewDueAt != nil && !candidate.ReviewDueAt.After(now) {
		score += 60
	}
	if candidate.ParentPriority {
		score += 30
	}
	if reviewOnly && isReview(candidate, now) {
		score += 200
	}
	return score
}

func isReview(candidate Candidate, now time.Time) bool {
	return candidate.ActiveMisconception || (candidate.ReviewDueAt != nil && !candidate.ReviewDueAt.After(now))
}

func classify(candidate Candidate, now time.Time) (Mode, string) {
	if candidate.CrossSubjectGap {
		return ModeMicroBacktrack, "cross_subject_prerequisite_gap"
	}
	if candidate.ActiveMisconception {
		return ModeRemediation, "active_misconception"
	}
	if candidate.ReviewDueAt != nil && !candidate.ReviewDueAt.After(now) {
		return ModeReview, "spaced_review_due"
	}
	return ModeCurrentGrade, "current_grade_progress"
}

func day(value time.Time) time.Time {
	year, month, date := value.In(planLocation).Date()
	return time.Date(year, month, date, 0, 0, 0, 0, planLocation)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

type ReplanAction string

const (
	Continue               ReplanAction = "CONTINUE"
	ReduceDifficulty       ReplanAction = "REDUCE_DIFFICULTY"
	InsertMicroRemediation ReplanAction = "INSERT_MICRO_REMEDIATION"
	SwitchScenario         ReplanAction = "SWITCH_SCENARIO"
	StopNewContent         ReplanAction = "STOP_NEW_CONTENT"
	EndEarly               ReplanAction = "END_EARLY"
)

type SessionSignal struct {
	EmotionLow       bool
	EngagementLow    bool
	RepeatedFailure  bool
	PrerequisiteGap  bool
	MinutesRemaining int
}

func (Engine) Replan(signal SessionSignal) ReplanAction {
	if signal.EmotionLow && signal.EngagementLow {
		return EndEarly
	}
	if signal.EmotionLow {
		return StopNewContent
	}
	if signal.PrerequisiteGap {
		return InsertMicroRemediation
	}
	if signal.RepeatedFailure {
		return SwitchScenario
	}
	if signal.EngagementLow {
		return ReduceDifficulty
	}
	return Continue
}
