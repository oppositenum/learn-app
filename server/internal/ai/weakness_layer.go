package ai

import (
	"errors"
	"fmt"
	"strings"
)

// WeaknessLayerNone is what a correct answer carries. It is never stored; a
// correct analysis persists a NULL layer.
const WeaknessLayerNone = "NONE"

var weaknessLayers = map[string]struct{}{"L1": {}, "L2": {}, "L3": {}, "L4": {}, "L5": {}, "L6": {}}

// ErrInvalidWeaknessLayer marks an analysis whose layer does not agree with
// its correctness. It wraps ErrInvalidProviderOutput so the provider retry
// treats it as a rejected sample, not as a transport failure.
var ErrInvalidWeaknessLayer = fmt.Errorf("%w: validate structured output at /weakness_layer", ErrInvalidProviderOutput)

// ValidWeaknessLayer reports whether layer is one of L1 to L6.
func ValidWeaknessLayer(layer string) bool {
	_, ok := weaknessLayers[layer]
	return ok
}

// ValidateWeaknessLayer requires a wrong answer to name one of L1 to L6 and a
// correct answer to carry NONE. A missing or unknown layer is rejected rather
// than filled in: defaulting to L1 would send the child back to prerequisites
// on a guess.
func ValidateWeaknessLayer(result AnalyzeAnswerResult) error {
	if result.AnswerCorrect {
		if result.WeaknessLayer != WeaknessLayerNone {
			return ErrInvalidWeaknessLayer
		}
		return nil
	}
	if !ValidWeaknessLayer(result.WeaknessLayer) {
		return ErrInvalidWeaknessLayer
	}
	return nil
}

// weaknessLayerPreamble and weaknessLayerMoves split weakness_layer.instructions.txt
// into its opening sentence and one move per layer, so a turn is told only the
// move for the layer it was diagnosed with.
var weaknessLayerPreamble, weaknessLayerMoves = parseWeaknessLayerInstructions(generationPrompt("weakness_layer.instructions.txt"))

func parseWeaknessLayerInstructions(text string) (string, map[string]string) {
	lines := strings.Split(text, "\n")
	moves := map[string]string{}
	for _, line := range lines[1:] {
		layer, move, found := strings.Cut(line, ":")
		if !found || !ValidWeaknessLayer(layer) {
			panic(fmt.Sprintf("weakness_layer.instructions.txt: malformed line %q", line))
		}
		moves[layer] = strings.TrimSpace(move)
	}
	if len(moves) != len(weaknessLayers) {
		panic(errors.New("weakness_layer.instructions.txt must define every layer L1 to L6"))
	}
	return lines[0], moves
}

// weaknessLayerInstructions returns the instruction that makes a turn follow
// the move for layer, or "" when no layer applies.
func weaknessLayerInstructions(layer string) string {
	move, ok := weaknessLayerMoves[layer]
	if !ok {
		return ""
	}
	return fmt.Sprintf("%s weakness_layer is %s: %s", weaknessLayerPreamble, layer, move)
}
