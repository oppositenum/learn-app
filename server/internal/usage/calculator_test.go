package usage

import "testing"

func TestCalculateSeparatesCachedAndUncachedTokensExactly(t *testing.T) {
	cost, err := Calculate(Price{InputPerMillion: "2.5", CachedInputPerMillion: "1.0", OutputPerMillion: "10"}, Amounts{InputTokens: 1000, CachedInputTokens: 400, OutputTokens: 200})
	if err != nil {
		t.Fatal(err)
	}
	if got := cost.FloatString(9); got != "0.003900000" {
		t.Fatalf("cost = %s", got)
	}
}

func TestCalculateAudioPerMinute(t *testing.T) {
	cost, err := Calculate(Price{InputPerMillion: "0", CachedInputPerMillion: "0", OutputPerMillion: "0", AudioInputPerMinute: "0.06", AudioOutputPerMinute: "0.12"}, Amounts{AudioInputSeconds: "30", AudioOutputSeconds: "15"})
	if err != nil {
		t.Fatal(err)
	}
	if got := cost.FloatString(9); got != "0.060000000" {
		t.Fatalf("audio cost = %s", got)
	}
}
