package reward

import "testing"

func TestRewardsAreDeterministicNonNegativeAndIdempotent(t *testing.T) {
	engine := Engine{}
	growth, created, err := engine.Apply(Growth{}, Event{Type: SelfCorrection, SourceID: "answer-1", Points: -100})
	if err != nil || !created || growth.TotalEnergy != 5 {
		t.Fatalf("first reward = %+v, %v, %v", growth, created, err)
	}
	growth, created, err = engine.Apply(growth, Event{Type: SelfCorrection, SourceID: "answer-1"})
	if err != nil || created || growth.TotalEnergy != 5 {
		t.Fatalf("duplicate reward = %+v, %v, %v", growth, created, err)
	}
}
