package ai

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/oppositenum/ai-learning-tutor/server/internal/content"
	"github.com/oppositenum/ai-learning-tutor/server/internal/studentinteraction"
	"github.com/oppositenum/ai-learning-tutor/server/internal/tutor"
)

const TutorMaterialPolicyVersion = "tutor-number-material-v1"

var ErrTutorMaterialPolicyViolation = errors.New("Tutor output violates the number material policy")

func enforceTutorMaterialPolicy(question content.QuestionPublic, turn TutorTurn) error {
	switch turn.Action {
	case tutor.StateProbe, tutor.StateHint, tutor.StateScaffold, tutor.StateAnalogy:
		allowed := numericMaterial(questionMaterialText(question))
		for token := range numericMaterial(turnText(turn)) {
			if _, ok := allowed[token]; !ok {
				return fmt.Errorf("%w: action %s introduced numeric material", ErrTutorMaterialPolicyViolation, turn.Action)
			}
		}
	}
	return nil
}

func questionMaterialText(question content.QuestionPublic) string {
	parts := []string{question.Prompt}
	scene, ok := studentinteraction.Parse(question.Scene, question.InputSchema)
	if !ok {
		return strings.Join(parts, "\n")
	}
	parts = append(parts, scene.AccessibleFallback)
	appendLabels := func(items []studentinteraction.Item) {
		for _, item := range items {
			parts = append(parts, item.Label)
		}
	}
	appendLabels(scene.Options)
	appendLabels(scene.Items)
	appendLabels(scene.Left)
	appendLabels(scene.Right)
	appendLabels(scene.Groups)
	appendLabels(scene.Slots)
	if scene.NumberLine != nil {
		parts = append(parts, scene.NumberLine.Label,
			strconv.FormatFloat(scene.NumberLine.Min, 'g', -1, 64),
			strconv.FormatFloat(scene.NumberLine.Max, 'g', -1, 64),
			strconv.FormatFloat(scene.NumberLine.Step, 'g', -1, 64))
	}
	return strings.Join(parts, "\n")
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
		if isChineseNumeralRune(current) {
			start := index
			for index < len(runes) {
				if isChineseNumeralRune(runes[index]) {
					index++
					continue
				}
				if runes[index] == '点' && index > start && index+1 < len(runes) && isChineseNumeralRune(runes[index+1]) {
					index++
					continue
				}
				break
			}
			token := string(runes[start:index])
			// Ordinal prose describes sequence rather than task material.
			if (start == 0 || runes[start-1] != '第') && chineseNumeralHasMaterialContext(runes, start, index) {
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

func isChineseNumeralRune(value rune) bool {
	return strings.ContainsRune("零〇一二两三四五六七八九十百千万亿半", value)
}

func chineseNumeralHasMaterialContext(runes []rune, start, end int) bool {
	if end-start > 1 || strings.ContainsRune(string(runes[start:end]), '点') {
		return true
	}
	if end < len(runes) && strings.ContainsRune("个只支盒杯元角分毫厘米克斤吨秒时天周月年次道题人本张页辆件份组排行列步倍层段颗朵条", runes[end]) {
		return true
	}
	if adjacentToNumericOperator(runes, start, end) {
		return true
	}
	prefixStart := max(0, start-6)
	prefix := string(runes[prefixStart:start])
	for _, cue := range []string{"数字", "数值", "数量", "等于", "读作", "写作", "标出", "找到", "选择", "计算"} {
		if strings.HasSuffix(prefix, cue) {
			return true
		}
	}
	return start == 0 && end == len(runes)
}

func adjacentToNumericOperator(runes []rune, start, end int) bool {
	const operators = "+-−×÷=<>%/＋－＝＜＞％加减乘除比和"
	return start > 0 && strings.ContainsRune(operators, runes[start-1]) ||
		end < len(runes) && strings.ContainsRune(operators, runes[end])
}
