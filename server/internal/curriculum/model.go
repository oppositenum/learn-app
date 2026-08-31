package curriculum

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/oppositenum/ai-learning-tutor/server/internal/subject"
)

type GradeBand string

const (
	GradeBandPrimary         GradeBand = "PRIMARY"
	GradeBandJuniorSecondary GradeBand = "JUNIOR_SECONDARY"
)

type Difficulty string

const (
	DifficultyL0 Difficulty = "L0"
	DifficultyL1 Difficulty = "L1"
	DifficultyL2 Difficulty = "L2"
	DifficultyL3 Difficulty = "L3"
	DifficultyL4 Difficulty = "L4"
	DifficultyL5 Difficulty = "L5"
)

type Subject struct {
	ID        uuid.UUID
	Code      subject.Code
	NameZH    string
	SortOrder int16
}

type Domain struct {
	ID          uuid.UUID
	SubjectID   uuid.UUID
	Code        string
	Name        string
	Description string
	SortOrder   int
}

type Unit struct {
	ID        uuid.UUID
	DomainID  uuid.UUID
	Code      string
	Name      string
	GradeBand GradeBand
	SortOrder int
}

type KnowledgePoint struct {
	ID                uuid.UUID
	SubjectID         uuid.UUID
	DomainID          uuid.UUID
	UnitID            uuid.UUID
	GradeBand         GradeBand
	Code              string
	Name              string
	Description       string
	WhyItMatters      json.RawMessage
	DefaultDifficulty Difficulty
	Status            string
	CurriculumVersion string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type KnowledgeDependency struct {
	SourceKnowledgePointID uuid.UUID
	TargetKnowledgePointID uuid.UUID
	Relation               string
	Strength               float64
}

type CrossSubjectDependency struct {
	ID                     uuid.UUID
	SourceKnowledgePointID uuid.UUID
	TargetKnowledgePointID uuid.UUID
	Relation               string
	Strength               float64
	FailureSignals         json.RawMessage
	RemediationPolicy      json.RawMessage
}

type CoreAbility struct {
	ID          uuid.UUID
	Code        string
	Name        string
	Description string
}

type Misconception struct {
	ID               uuid.UUID
	Code             string
	Name             string
	Description      string
	DiagnosisSignals json.RawMessage
}
