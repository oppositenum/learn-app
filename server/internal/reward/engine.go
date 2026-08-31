package reward

import "errors"

type Type string

const (
	Effort              Type = "EFFORT"
	SelfCorrection      Type = "SELF_CORRECTION"
	HintSuccess         Type = "HINT_SUCCESS"
	Mastery             Type = "MASTERY"
	DelayedReview       Type = "DELAYED_REVIEW"
	DailyCompletion     Type = "DAILY_COMPLETION"
	CrossSubjectInsight Type = "CROSS_SUBJECT_INSIGHT"
)

var ErrUnknownReward = errors.New("unknown reward type")

var points = map[Type]int{
	Effort: 2, SelfCorrection: 5, HintSuccess: 3, Mastery: 12,
	DelayedReview: 8, DailyCompletion: 10, CrossSubjectInsight: 6,
}

type Event struct {
	Type     Type
	SourceID string
	Points   int
}
type Growth struct {
	TotalEnergy int
	Applied     map[string]struct{}
}

type Engine struct{}

func (Engine) Apply(growth Growth, event Event) (Growth, bool, error) {
	award, ok := points[event.Type]
	if !ok {
		return growth, false, ErrUnknownReward
	}
	if event.SourceID == "" {
		return growth, false, errors.New("reward source ID is required")
	}
	if growth.Applied == nil {
		growth.Applied = make(map[string]struct{})
	}
	key := string(event.Type) + ":" + event.SourceID
	if _, exists := growth.Applied[key]; exists {
		return growth, false, nil
	}
	event.Points = award
	growth.TotalEnergy += event.Points
	growth.Applied[key] = struct{}{}
	return growth, true, nil
}
