package tutor

const DefaultMaxSocraticFailedRounds = 3

type Emotion string

const (
	EmotionNeutral    Emotion = "NEUTRAL"
	EmotionFrustrated Emotion = "FRUSTRATED"
	EmotionBored      Emotion = "BORED"
)

type ReasoningQuality string

const (
	ReasoningStrong  ReasoningQuality = "STRONG"
	ReasoningPartial ReasoningQuality = "PARTIAL"
	ReasoningWeak    ReasoningQuality = "WEAK"
)

type Session struct {
	State                State
	SocraticFailedRounds int
	OriginalTaskID       string
	ActiveTaskID         string
	BacktrackTaskID      string
}

type Analysis struct {
	AnswerCorrect    bool
	ReasoningQuality ReasoningQuality
	PrerequisiteGap  bool
	Emotion          Emotion
	HintRequested    bool
	DontKnow         bool
	VoicePreferred   bool
}

type Decision struct {
	NextState            State
	SocraticRound        int
	AnswerRevealAllowed  bool
	PreserveOriginalTask bool
	ReturnToOriginalTask bool
	Reason               string
}

type Engine struct {
	MaxSocraticFailedRounds int
}

func NewEngine(maxFailedRounds int) Engine {
	if maxFailedRounds <= 0 {
		maxFailedRounds = DefaultMaxSocraticFailedRounds
	}
	return Engine{MaxSocraticFailedRounds: maxFailedRounds}
}

func (engine Engine) Decide(session Session, analysis Analysis) Decision {
	if analysis.AnswerCorrect {
		return Decision{NextState: StateVariant, Reason: "answer understood; verify with a variant"}
	}

	if analysis.Emotion == EmotionFrustrated || analysis.Emotion == EmotionBored {
		if analysis.VoicePreferred {
			return Decision{NextState: StateVoiceExplain, Reason: "deescalate with a short voice explanation"}
		}
		return Decision{NextState: StateBreak, Reason: "emotion signal takes priority over further probing"}
	}

	if analysis.PrerequisiteGap {
		return Decision{
			NextState:            StateBacktrack,
			PreserveOriginalTask: true,
			Reason:               "remediate a prerequisite and preserve the original task",
		}
	}

	if analysis.HintRequested || analysis.DontKnow {
		return Decision{NextState: StateHint, Reason: "student requested a bounded hint"}
	}

	if session.SocraticFailedRounds >= engine.MaxSocraticFailedRounds {
		next := StateExplain
		if analysis.VoicePreferred {
			next = StateVoiceExplain
		}
		return Decision{
			NextState:           next,
			SocraticRound:       engine.MaxSocraticFailedRounds,
			AnswerRevealAllowed: false,
			Reason:              "Socratic failed-round limit reached; explain with a parallel example",
		}
	}

	round := session.SocraticFailedRounds + 1
	switch round {
	case 1:
		return Decision{NextState: StateProbe, SocraticRound: round, Reason: "inspect the current reasoning"}
	case 2:
		return Decision{NextState: StateScaffold, SocraticRound: round, Reason: "split the task into a smaller step"}
	default:
		return Decision{NextState: StateAnalogy, SocraticRound: round, Reason: "switch to a concrete life analogy"}
	}
}

func (engine Engine) Apply(session Session, decision Decision) Session {
	session.State = decision.NextState
	if decision.SocraticRound > session.SocraticFailedRounds {
		session.SocraticFailedRounds = decision.SocraticRound
	}
	if decision.PreserveOriginalTask && session.OriginalTaskID == "" {
		session.OriginalTaskID = session.ActiveTaskID
	}
	if decision.ReturnToOriginalTask && session.OriginalTaskID != "" {
		session.ActiveTaskID = session.OriginalTaskID
		session.BacktrackTaskID = ""
	}
	return session
}

func ReturnFromBacktrack(session Session) Decision {
	return Decision{
		NextState:            StateReturn,
		ReturnToOriginalTask: true,
		Reason:               "prerequisite remediation completed; return to original task",
	}
}
