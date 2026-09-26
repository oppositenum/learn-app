package classroom

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
	"github.com/oppositenum/ai-learning-tutor/server/internal/subject"
	"github.com/oppositenum/ai-learning-tutor/server/internal/tutor"
)

type Stage string

const (
	StageOriginal Stage = "ORIGINAL"
	StageVariant  Stage = "VARIANT"
	StageAbstract Stage = "ABSTRACT"
	StageVerify   Stage = "VERIFY"
	StageComplete Stage = "COMPLETE"
)

var orderedStages = []Stage{StageOriginal, StageVariant, StageAbstract, StageVerify}

func (stage Stage) Valid() bool {
	for _, candidate := range orderedStages {
		if stage == candidate {
			return true
		}
	}
	return false
}

func nextStage(stage Stage) (Stage, bool) {
	switch stage {
	case StageOriginal:
		return StageVariant, true
	case StageVariant:
		return StageAbstract, true
	case StageAbstract:
		return StageVerify, true
	case StageVerify:
		return StageComplete, true
	default:
		return "", false
	}
}

type StageAttemptKind string

const (
	StageAttemptAnswer StageAttemptKind = "ANSWER"
	StageAttemptHelp   StageAttemptKind = "HELP"
)

type StageScore string

const (
	StageScoreCorrect       StageScore = "CORRECT"
	StageScoreIncorrect     StageScore = "INCORRECT"
	StageScoreIndeterminate StageScore = "INDETERMINATE"
	StageScoreHelpRequested StageScore = "HELP_REQUESTED"
)

type StageEvidenceKind string

const (
	StageEvidenceNone        StageEvidenceKind = "NONE"
	StageEvidenceAssisted    StageEvidenceKind = "ASSISTED"
	StageEvidenceIndependent StageEvidenceKind = "INDEPENDENT"
)

const (
	stageScoringExactOptionSetV1 = "exact-option-set-v1"
	stageScoringExactOrderV1     = "exact-order-v1"
	stageScoringExactPairsV1     = "exact-pairs-v1"
	stageScoringExactGroupingV1  = "exact-grouping-v1"
	stageScoringExactNumberV1    = "exact-number-v1"
	stageScoringExactFillV1      = "exact-fill-v1"
)

const (
	stageFlowVersion                = "classroom-stage-flow-v1"
	stageMasteryAuthorizationPolicy = "deterministic-stage-evidence-v1"
)

var (
	ErrStagePreparationIncomplete = errors.New("four-stage classroom content is incomplete")
	ErrStageUnavailable           = errors.New("four-stage classroom task is unavailable")
	ErrStageContentExhausted      = errors.New("four-stage classroom has no unpresented task")
	ErrStageMismatch              = errors.New("four-stage classroom is out of order")
	ErrStageTaskMismatch          = errors.New("four-stage classroom task does not match")
	ErrStageVersionDrift          = errors.New("four-stage classroom task version changed")
	ErrStageOperationConflict     = errors.New("operation id was reused with different input")
	ErrStageSessionComplete       = errors.New("four-stage classroom is complete")
	ErrStageResponseRequired      = errors.New("structured response is required")
)

type stageTask struct {
	ID                 uuid.UUID
	LineageID          uuid.UUID
	KnowledgePointID   uuid.UUID
	SubjectCode        subject.Code
	Stage              Stage
	SelectionOrder     int
	ContentVersion     string
	ScoringRuleVersion string
	ScoringRule        json.RawMessage
	EvidenceForm       string
	Prompt             string
	Scene              json.RawMessage
	InputSchema        json.RawMessage
}

type StageSubmitRequest struct {
	SessionID   uuid.UUID
	OperationID uuid.UUID
	Stage       Stage
	TaskID      uuid.UUID
	TaskVersion string
	Kind        StageAttemptKind
	Support     SupportType
	Response    json.RawMessage
}

type StageSubmitResult struct {
	SessionID           uuid.UUID         `json:"session_id"`
	Version             int64             `json:"version"`
	TimingVersion       int64             `json:"timing_version"`
	Action              tutor.State       `json:"action"`
	Stage               Stage             `json:"stage"`
	TaskID              *uuid.UUID        `json:"task_id,omitempty"`
	TaskVersion         string            `json:"task_version,omitempty"`
	DeterministicResult StageScore        `json:"-"`
	EvidenceKind        StageEvidenceKind `json:"evidence_kind"`
	StageCompleted      bool              `json:"stage_completed"`
	Code                string            `json:"code,omitempty"`
	Message             string            `json:"message"`
	SocraticRound       int               `json:"socratic_round"`
	Status              string            `json:"status"`
	ActiveSeconds       int               `json:"active_seconds"`
	CurrentSeconds      int               `json:"current_active_seconds"`
	TimingAt            time.Time         `json:"timing_observed_at"`
	Feedback            *ai.TutorTurn     `json:"-"`
	Safety              *SafetyNotice     `json:"safety,omitempty"`
}

type StudentStageFlow struct {
	Version     string `json:"version"`
	Stage       Stage  `json:"stage"`
	TaskVersion string `json:"task_version"`
}

func stageMessage(code string) string {
	switch code {
	case "TRY_NEW_TASK":
		return "这次还没有通过确定性检查，换一道不重复的新任务再试。"
	case "SOCRATIC_GUIDED":
		return "这次还没有通过确定性检查，先按这条引导再想一步，还是这道题。"
	case "HELP_DELIVERED":
		return "先按这条提示完成当前任务；完成后还要用新任务独立证明。"
	case "ASSISTED_REPROOF":
		return "当前任务在帮助下完成，现在换一道不重复的新任务独立证明。"
	case "SOCRATIC_LIMIT_EXPLAINED":
		return "已经完成三次有效尝试。先看同结构示范，再回到当前任务验证。"
	case "SOCRATIC_REPROOF_FAILED":
		return "看过示范后这次还没有独立通过。当前课堂已结束，稍后可以重新开始。"
	case "NEXT_STAGE":
		return "这一阶段已独立完成，继续下一阶段。"
	case "CLASSROOM_COMPLETE":
		return "变式、抽象和验证都已真实完成。"
	case "CONTENT_EXHAUSTED":
		return "当前阶段暂时没有新的不重复任务。这次课堂已安全结束，可以稍后重新开始。"
	default:
		return "继续完成当前阶段。"
	}
}
