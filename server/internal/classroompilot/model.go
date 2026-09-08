package classroompilot

import (
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
	"github.com/oppositenum/ai-learning-tutor/server/internal/subject"
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

func NextStage(stage Stage) (Stage, bool) {
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

func CanAdvance(from, to Stage) bool {
	next, ok := NextStage(from)
	return ok && next == to
}

type AttemptKind string

const (
	AttemptAnswer AttemptKind = "ANSWER"
	AttemptHelp   AttemptKind = "HELP"
)

type DeterministicResult string

const (
	ResultCorrect       DeterministicResult = "CORRECT"
	ResultIncorrect     DeterministicResult = "INCORRECT"
	ResultIndeterminate DeterministicResult = "INDETERMINATE"
	ResultHelpRequested DeterministicResult = "HELP_REQUESTED"
)

type EvidenceKind string

const (
	EvidenceNone        EvidenceKind = "NONE"
	EvidenceAssisted    EvidenceKind = "ASSISTED"
	EvidenceIndependent EvidenceKind = "INDEPENDENT"
)

const (
	ScoringRuleExactOptionSetV1 = "exact-option-set-v1"

	MessagePreparationIncomplete = "这节课还在准备中，请稍后再来看看"
	MessageStageUnavailable      = "这一步暂时没有准备好，请稍后再试"
)

var (
	ErrPreparationIncomplete = errors.New("B4 pilot content preflight failed")
	ErrStageUnavailable      = errors.New("B4 pilot current stage is unavailable")
	ErrSessionNotFound       = errors.New("B4 pilot session not found")
	ErrSessionComplete       = errors.New("B4 pilot session is complete")
	ErrStageMismatch         = errors.New("B4 pilot stage is out of order")
	ErrTaskMismatch          = errors.New("B4 pilot task does not match current stage")
	ErrVersionDrift          = errors.New("B4 pilot task version changed")
	ErrDuplicateSuccess      = errors.New("B4 pilot task was already completed")
	ErrOperationConflict     = errors.New("B4 pilot operation id was reused with different input")
	ErrClassroomChanged      = errors.New("B4 pilot classroom changed during feedback")
	ErrFeedbackUnavailable   = errors.New("B4 pilot audited feedback is unavailable")
)

type Task struct {
	ID                 uuid.UUID
	LineageID          uuid.UUID
	KnowledgePointID   uuid.UUID
	SubjectCode        subject.Code
	Stage              Stage
	SelectionOrder     int
	ContentVersion     string
	ScoringRuleVersion string
	ScoringRule        json.RawMessage
}

type StartRequest struct {
	StudentID        uuid.UUID
	KnowledgePointID uuid.UUID
	TargetMinutes    int
}

type StartResult struct {
	SessionID   uuid.UUID
	Stage       Stage
	TaskID      uuid.UUID
	TaskVersion string
	Message     string
}

type SubmitRequest struct {
	SessionID   uuid.UUID
	OperationID uuid.UUID
	Stage       Stage
	TaskID      uuid.UUID
	TaskVersion string
	Kind        AttemptKind
	Response    json.RawMessage
}

type SubmitResult struct {
	SessionID           uuid.UUID
	Stage               Stage
	TaskID              uuid.UUID
	TaskVersion         string
	DeterministicResult DeterministicResult
	EvidenceKind        EvidenceKind
	StageCompleted      bool
	Complete            bool
	Message             string
	ControlsEnabled     bool
	PreserveInput       bool
	Feedback            *ai.TutorTurn
}

func fixedMessage(code string) string {
	switch code {
	case "TRY_AGAIN":
		return "再检查题目要求的信息类别，然后重新选择。"
	case "HELP_DELIVERED":
		return "先按老师的提示想一想，再完成当前任务。"
	case "ASSISTED_REPROOF":
		return "这次是在帮助下完成的，请用同一阶段的新任务再独立证明一次。"
	case "NEXT_STAGE":
		return "这一阶段已完成，继续下一阶段。"
	case "PILOT_COMPLETE":
		return "四个阶段都已完成。"
	default:
		return ""
	}
}
