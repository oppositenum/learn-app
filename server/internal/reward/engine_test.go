package reward

import "testing"

func TestRewardsAreDeterministicNonNegativeAndIdempotent(t *testing.T) {
	engine := Engine{}
	growth, created, err := engine.Apply(Growth{}, Event{Type: SelfCorrection, SourceID: "answer-1", Points: -100})
	if err != nil || !created || growth.TotalEnergy != 8 {
		t.Fatalf("first reward = %+v, %v, %v", growth, created, err)
	}
	growth, created, err = engine.Apply(growth, Event{Type: SelfCorrection, SourceID: "answer-1"})
	if err != nil || created || growth.TotalEnergy != 8 {
		t.Fatalf("duplicate reward = %+v, %v, %v", growth, created, err)
	}
}

func TestRewardsFollowTheGrowthPointTable(t *testing.T) {
	table := map[Type]int{
		Effort: 3, SelfCorrection: 8, HintSuccess: 6, DelayedReview: 12,
		Mastery: 20, CrossSubjectInsight: 10, DailyCompletion: 15,
	}
	retired := map[Type]int{
		Effort: 2, SelfCorrection: 5, HintSuccess: 3, Mastery: 12,
		DelayedReview: 8, DailyCompletion: 10, CrossSubjectInsight: 6,
	}
	if len(points) != len(table) {
		t.Fatalf("reward types=%d want %d", len(points), len(table))
	}
	for rewardType, want := range table {
		growth, created, err := (Engine{}).Apply(Growth{}, Event{Type: rewardType, SourceID: "source"})
		if err != nil || !created || growth.TotalEnergy != want {
			t.Fatalf("%s = %d, %v, %v; want %d", rewardType, growth.TotalEnergy, created, err, want)
		}
		if growth.TotalEnergy == retired[rewardType] {
			t.Fatalf("%s still grants the retired value %d", rewardType, retired[rewardType])
		}
	}
	for _, rewardType := range []Type{"EXPLAINED_REASONING", "STREAK_BONUS"} {
		if _, _, err := (Engine{}).Apply(Growth{}, Event{Type: rewardType, SourceID: "source"}); err != ErrUnknownReward {
			t.Fatalf("%s err=%v; this reward is not part of V1", rewardType, err)
		}
	}
}
