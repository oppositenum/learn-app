package tutor

import "testing"

func TestSocraticLimitUsesThreeEffectiveAttemptsThenExplains(t *testing.T) {
	engine := NewEngine(0)
	session := Session{State: StateAnalyze, ActiveTaskID: "original"}
	want := []State{StateProbe, StateScaffold, StateAnalogy, StateExplain}

	for index, wantState := range want {
		decision := engine.Decide(session, Analysis{ReasoningQuality: ReasoningWeak})
		if decision.NextState != wantState {
			t.Fatalf("decision %d: expected %s, got %s", index+1, wantState, decision.NextState)
		}
		if decision.AnswerRevealAllowed {
			t.Fatalf("decision %d unexpectedly allowed answer reveal", index+1)
		}
		session = engine.Apply(session, decision)
	}
	if session.SocraticFailedRounds != DefaultMaxSocraticFailedRounds {
		t.Fatalf("failed rounds = %d, want %d", session.SocraticFailedRounds, DefaultMaxSocraticFailedRounds)
	}
}

func TestHelpRequestDoesNotConsumeAFailedRound(t *testing.T) {
	session := Session{State: StateAnalyze, SocraticFailedRounds: 0}
	decision := NewEngine(0).Decide(session, Analysis{DontKnow: true})
	if decision.NextState != StateHint {
		t.Fatalf("expected HINT, got %s", decision.NextState)
	}
	if decision.SocraticRound != 0 {
		t.Fatalf("help request consumed a Socratic round: %d", decision.SocraticRound)
	}
	session = NewEngine(0).Apply(session, decision)
	if session.SocraticFailedRounds != 0 {
		t.Fatalf("applied help request advanced failed rounds: %d", session.SocraticFailedRounds)
	}
}

func TestEmotionSignalShortCircuitsSocraticLoop(t *testing.T) {
	decision := NewEngine(0).Decide(Session{}, Analysis{Emotion: EmotionFrustrated})
	if decision.NextState != StateBreak {
		t.Fatalf("expected BREAK, got %s", decision.NextState)
	}
	if decision.SocraticRound != 0 {
		t.Fatalf("emotion deescalation consumed a Socratic round: %d", decision.SocraticRound)
	}
}

func TestCrossSubjectBacktrackReturnsToOriginalTask(t *testing.T) {
	engine := NewEngine(0)
	session := Session{State: StateAnalyze, ActiveTaskID: "physics-speed"}
	decision := engine.Decide(session, Analysis{PrerequisiteGap: true})
	session = engine.Apply(session, decision)
	if session.OriginalTaskID != "physics-speed" {
		t.Fatalf("original task was not preserved: %+v", session)
	}

	session.ActiveTaskID = "math-ratio-supply"
	session.BacktrackTaskID = "math-ratio-supply"
	session = engine.Apply(session, ReturnFromBacktrack(session))
	if session.ActiveTaskID != "physics-speed" || session.BacktrackTaskID != "" {
		t.Fatalf("did not return to original task: %+v", session)
	}
}

func TestCorrectAnswerMovesToVariantNotMastery(t *testing.T) {
	decision := NewEngine(0).Decide(Session{}, Analysis{AnswerCorrect: true, ReasoningQuality: ReasoningStrong})
	if decision.NextState != StateVariant {
		t.Fatalf("expected VARIANT, got %s", decision.NextState)
	}
}

func TestCompleteTutorStateMachineIsReachableAndCompleteIsTerminal(t *testing.T) {
	reachable := map[State]bool{StateIntro: true}
	queue := []State{StateIntro}
	for len(queue) > 0 {
		from := queue[0]
		queue = queue[1:]
		for to := range allowedTransitions[from] {
			if !reachable[to] {
				reachable[to] = true
				queue = append(queue, to)
			}
		}
	}
	for state := range allStates {
		if !reachable[state] {
			t.Errorf("Tutor state %s is unreachable from INTRO", state)
		}
	}
	if CanTransition(StateComplete, StateAsk) {
		t.Fatal("COMPLETE unexpectedly has an outgoing transition")
	}
	if !CanTransition(StateVoiceExplain, StateReturn) || !CanTransition(StateBacktrack, StateReturn) {
		t.Fatal("voice or prerequisite remediation cannot return to verification")
	}
}
