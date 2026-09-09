package ai

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/oppositenum/ai-learning-tutor/server/internal/tutor"
)

const TutorMaterialPolicyVersion = "tutor-number-material-v1"

var ErrTutorMaterialPolicyViolation = errors.New("Tutor output violates the number material policy")

func enforceTutorMaterialPolicy(question string, turn TutorTurn) error {
	switch turn.Action {
	case tutor.StateProbe, tutor.StateHint, tutor.StateScaffold, tutor.StateAnalogy:
		allowed := numericMaterial(question)
		for token := range numericMaterial(turnText(turn)) {
			if _, ok := allowed[token]; !ok {
				return fmt.Errorf("%w: action %s introduced numeric material", ErrTutorMaterialPolicyViolation, turn.Action)
			}
		}
	}
	return nil
}

func ensureOriginalTaskVerification(turn TutorTurn) TutorTurn {
	if turn.Action != tutor.StateExplain && turn.Action != tutor.StateVoiceExplain {
		return turn
	}
	const returnPrompt = "现在回到原题，请你再独立试一次。"
	if !strings.Contains(turn.Message, returnPrompt) {
		turn.Message = strings.TrimSpace(turn.Message) + " " + returnPrompt
	}
	return turn
}

func turnText(turn TutorTurn) string {
	value := turn.Message
	for _, segment := range turn.Segments {
		value += "\n" + segment.Text
	}
	return value
}

func numericMaterial(value string) map[string]struct{} {
	result := map[string]struct{}{}
	runes := []rune(value)
	for index := 0; index < len(runes); {
		current := normalizeDigit(runes[index])
		if current >= '0' && current <= '9' {
			var token strings.Builder
			for index < len(runes) {
				current = normalizeDigit(runes[index])
				if current >= '0' && current <= '9' {
					token.WriteRune(current)
					index++
					continue
				}
				if (current == '.' || current == '/' || current == '%' || current == ':') && index+1 < len(runes) {
					next := normalizeDigit(runes[index+1])
					if next >= '0' && next <= '9' {
						token.WriteRune(current)
						index++
						continue
					}
				}
				break
			}
			result[token.String()] = struct{}{}
			continue
		}
		if isChineseNumberRune(current) {
			start := index
			for index < len(runes) && isChineseNumberRune(runes[index]) {
				index++
			}
			token := string(runes[start:index])
			// Single Chinese number characters occur frequently in prose such as
			// "第一步". Multi-character forms still catch substituted values.
			if len([]rune(token)) > 1 {
				result[token] = struct{}{}
			}
			continue
		}
		index++
	}
	return result
}

func normalizeDigit(value rune) rune {
	if value >= '０' && value <= '９' {
		return '0' + value - '０'
	}
	if value == '％' {
		return '%'
	}
	return unicode.ToLower(value)
}

func isChineseNumberRune(value rune) bool {
	return strings.ContainsRune("零〇一二两三四五六七八九十百千万亿点分之半", value)
}
