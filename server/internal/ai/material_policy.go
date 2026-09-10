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
			start := index
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
			if start == 0 || runes[start-1] != '第' {
				result[canonicalArabicNumber(token.String())] = struct{}{}
			}
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
				result[canonicalChineseNumber(token)] = struct{}{}
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
	if isProceduralChineseNumeral(runes, start, end) {
		return false
	}
	token := string(runes[start:end])
	if (token == "万一" || token == "千万") && !hasExplicitNumericContext(runes, start, end) {
		return false
	}
	if end-start > 1 || strings.ContainsRune(string(runes[start:end]), '点') {
		return true
	}
	if hasExplicitNumericContext(runes, start, end) {
		return true
	}
	return start == 0 && end == len(runes)
}

func hasExplicitNumericContext(runes []rune, start, end int) bool {
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
	return false
}

func adjacentToNumericOperator(runes []rune, start, end int) bool {
	const operators = "+-−×÷=<>%/＋－＝＜＞％加减乘除比和"
	return start > 0 && strings.ContainsRune(operators, runes[start-1]) ||
		end < len(runes) && strings.ContainsRune(operators, runes[end])
}

func isProceduralChineseNumeral(runes []rune, start, end int) bool {
	token := string(runes[start:end])
	if token == "一" && end < len(runes) && runes[end] == '个' {
		suffix := string(runes[end+1:])
		for _, noun := range []string{"角度", "例子", "思路", "方法", "办法", "方式"} {
			if strings.HasPrefix(suffix, noun) {
				return true
			}
		}
	}
	if token == "一" && end < len(runes) && runes[end] == '步' {
		if start > 0 && strings.ContainsRune("上下这每逐前后步", runes[start-1]) {
			return true
		}
		if end+1 < len(runes) && (runes[end+1] == '一' || runes[end+1] == '到') {
			return true
		}
	}
	if token == "一" && end < len(runes) && runes[end] == '次' && start > 0 && strings.ContainsRune("试想看做读说写查来复", runes[start-1]) {
		return true
	}
	if token == "十" && end+1 < len(runes) && runes[end] == '分' && runes[end+1] != '之' && !strings.ContainsRune("米秒钟", runes[end+1]) {
		return true
	}
	return false
}

func canonicalArabicNumber(token string) string {
	if strings.ContainsAny(token, "/%:") {
		return token
	}
	integer, decimal, found := strings.Cut(token, ".")
	if !allASCIIDigits(integer) || found && !allASCIIDigits(decimal) {
		return token
	}
	integer = strings.TrimLeft(integer, "0")
	if integer == "" {
		integer = "0"
	}
	if !found {
		return integer
	}
	decimal = strings.TrimRight(decimal, "0")
	if decimal == "" {
		return integer
	}
	return integer + "." + decimal
}

func canonicalChineseNumber(token string) string {
	if token == "半" || token == "一半" {
		return "0.5"
	}
	integer, decimal, found := strings.Cut(token, "点")
	value, ok := chineseInteger(integer)
	if !ok {
		return token
	}
	canonical := strconv.FormatInt(value, 10)
	if !found {
		return canonical
	}
	var fractional strings.Builder
	for _, value := range decimal {
		digit, ok := chineseDigit(value)
		if !ok {
			return token
		}
		fractional.WriteByte(byte('0' + digit))
	}
	fraction := strings.TrimRight(fractional.String(), "0")
	if fraction == "" {
		return canonical
	}
	return canonical + "." + fraction
}

func chineseInteger(value string) (int64, bool) {
	if value == "" {
		return 0, false
	}
	hasUnit := strings.ContainsAny(value, "十百千万亿")
	if !hasUnit {
		var digits strings.Builder
		for _, current := range value {
			digit, ok := chineseDigit(current)
			if !ok {
				return 0, false
			}
			digits.WriteByte(byte('0' + digit))
		}
		parsed, err := strconv.ParseInt(digits.String(), 10, 64)
		return parsed, err == nil
	}
	var total, section, number int64
	for _, current := range value {
		if digit, ok := chineseDigit(current); ok {
			number = int64(digit)
			continue
		}
		unit := chineseUnit(current)
		if unit == 0 {
			return 0, false
		}
		if unit < 10000 {
			if number == 0 {
				number = 1
			}
			section += number * unit
			number = 0
			continue
		}
		section += number
		if section == 0 {
			section = 1
		}
		total += section * unit
		section, number = 0, 0
	}
	return total + section + number, true
}

func chineseDigit(value rune) (int, bool) {
	switch value {
	case '零', '〇':
		return 0, true
	case '一':
		return 1, true
	case '二', '两':
		return 2, true
	case '三':
		return 3, true
	case '四':
		return 4, true
	case '五':
		return 5, true
	case '六':
		return 6, true
	case '七':
		return 7, true
	case '八':
		return 8, true
	case '九':
		return 9, true
	default:
		return 0, false
	}
}

func chineseUnit(value rune) int64 {
	switch value {
	case '十':
		return 10
	case '百':
		return 100
	case '千':
		return 1000
	case '万':
		return 10000
	case '亿':
		return 100000000
	default:
		return 0
	}
}

func allASCIIDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, current := range value {
		if current < '0' || current > '9' {
			return false
		}
	}
	return true
}
