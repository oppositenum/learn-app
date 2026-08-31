package tutor

import "fmt"

type State string

const (
	StateIntro        State = "INTRO"
	StateAsk          State = "ASK"
	StateWait         State = "WAIT"
	StateAnalyze      State = "ANALYZE"
	StateProbe        State = "PROBE"
	StateHint         State = "HINT"
	StateScaffold     State = "SCAFFOLD"
	StateAnalogy      State = "ANALOGY"
	StateBacktrack    State = "BACKTRACK"
	StateExplain      State = "EXPLAIN"
	StateVoiceExplain State = "VOICE_EXPLAIN"
	StateReturn       State = "RETURN"
	StateVariant      State = "VARIANT"
	StateAbstract     State = "ABSTRACT"
	StateVerify       State = "VERIFY"
	StateReview       State = "REVIEW"
	StateComplete     State = "COMPLETE"
	StateBreak        State = "BREAK"
)

var allStates = map[State]struct{}{
	StateIntro: {}, StateAsk: {}, StateWait: {}, StateAnalyze: {}, StateProbe: {},
	StateHint: {}, StateScaffold: {}, StateAnalogy: {}, StateBacktrack: {},
	StateExplain: {}, StateVoiceExplain: {}, StateReturn: {}, StateVariant: {},
	StateAbstract: {}, StateVerify: {}, StateReview: {}, StateComplete: {}, StateBreak: {},
}

var allowedTransitions = map[State]map[State]struct{}{
	StateIntro:        states(StateAsk, StateReview),
	StateReview:       states(StateAsk),
	StateAsk:          states(StateWait),
	StateWait:         states(StateAnalyze),
	StateAnalyze:      states(StateProbe, StateHint, StateScaffold, StateAnalogy, StateBacktrack, StateExplain, StateVoiceExplain, StateVariant, StateBreak),
	StateProbe:        states(StateWait),
	StateHint:         states(StateWait),
	StateScaffold:     states(StateWait),
	StateAnalogy:      states(StateWait),
	StateBacktrack:    states(StateWait, StateReturn),
	StateExplain:      states(StateWait, StateVoiceExplain),
	StateVoiceExplain: states(StateReturn),
	StateReturn:       states(StateWait, StateVariant),
	StateVariant:      states(StateAbstract),
	StateAbstract:     states(StateVerify),
	StateVerify:       states(StateComplete, StateReview),
	StateBreak:        states(StateAsk, StateComplete),
	StateComplete:     {},
}

func states(values ...State) map[State]struct{} {
	result := make(map[State]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func (state State) Valid() bool {
	_, ok := allStates[state]
	return ok
}

func ParseState(value string) (State, error) {
	state := State(value)
	if !state.Valid() {
		return "", fmt.Errorf("unsupported tutor state %q", value)
	}
	return state, nil
}

// CanTransition defines the complete conceptual Tutor lifecycle. WAIT and
// ANALYZE may be folded into one synchronous server request, but a persisted
// teaching action must still be reachable through this graph.
func CanTransition(from, to State) bool {
	toStates, ok := allowedTransitions[from]
	if !ok {
		return false
	}
	_, ok = toStates[to]
	return ok
}
