package contentpipeline

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"regexp"
	"sort"
	"strings"
)

var validSubjects = map[string]bool{"MATH": true, "CHINESE": true, "ENGLISH": true, "PHYSICS": true, "CHEMISTRY": true}
var validDifficulties = map[string]bool{"L0": true, "L1": true, "L2": true, "L3": true, "L4": true, "L5": true}

type DuplicateLookup interface {
	Exists(normalizedHash string, exceptQuestionID string) bool
}
type Validator struct{ Duplicates DuplicateLookup }

func (validator Validator) Validate(asset Asset) Validation {
	checks := []Check{
		check("schema", requiredValid(asset), "required fields, enums, JSON, and DRAFT state"),
		check("answer_not_public", !leaksAnswer(asset), "public prompt must not contain a standalone private answer"),
		check("numeric_answer", numericConsistent(asset), "numeric answer and solution result must agree"),
		check("unit", unitConsistent(asset), "unit-bearing numeric content must declare an expected unit"),
		check("choices", choicesValid(asset), "choice IDs/text must be unique with exactly one correct option"),
		check("solution", solutionConsistent(asset), "solution must be non-empty and consistent with the answer"),
		check("duplicate", !validator.duplicate(asset), "normalized prompt must not duplicate another asset"),
	}
	passed := true
	for _, item := range checks {
		passed = passed && item.Passed
	}
	return Validation{Passed: passed, Checks: checks}
}

func requiredValid(asset Asset) bool {
	if asset.QuestionID == "" || asset.KnowledgePointID == "" || asset.PromptPublic == "" || asset.TeacherPrivate.Answer == "" || asset.SourceID == "" || asset.ContentVersion == "" {
		return false
	}
	if asset.SchemaVersion != CurrentSchemaVersion || asset.Status != Draft || !validSubjects[asset.SubjectCode] || !validDifficulties[asset.Difficulty] {
		return false
	}
	var schema any
	return len(asset.InputSchema) > 0 && json.Unmarshal(asset.InputSchema, &schema) == nil
}

func leaksAnswer(asset Asset) bool {
	answer := strings.TrimSpace(strings.ToLower(asset.TeacherPrivate.Answer))
	if answer == "" {
		return false
	}
	pattern := regexp.MustCompile(`(^|[^\pL\pN.])` + regexp.QuoteMeta(answer) + `([^\pL\pN.]|$)`)
	return pattern.MatchString(strings.ToLower(asset.PromptPublic))
}

func numericConsistent(asset Asset) bool {
	if asset.TeacherPrivate.NumericValue == nil && asset.TeacherPrivate.SolutionResult == nil {
		return true
	}
	if asset.TeacherPrivate.NumericValue == nil || asset.TeacherPrivate.SolutionResult == nil {
		return false
	}
	return math.Abs(*asset.TeacherPrivate.NumericValue-*asset.TeacherPrivate.SolutionResult) < 1e-9
}

func unitConsistent(asset Asset) bool {
	if asset.TeacherPrivate.NumericValue == nil {
		return true
	}
	unit := strings.TrimSpace(asset.TeacherPrivate.Unit)
	if asset.SubjectCode == "PHYSICS" || asset.SubjectCode == "CHEMISTRY" {
		return unit != ""
	}
	return true
}

func choicesValid(asset Asset) bool {
	if asset.QuestionType != "MULTIPLE_CHOICE" {
		return len(asset.Choices) == 0
	}
	if len(asset.Choices) < 2 {
		return false
	}
	ids, texts, correct := map[string]bool{}, map[string]bool{}, 0
	for _, choice := range asset.Choices {
		text := strings.TrimSpace(strings.ToLower(choice.Text))
		if choice.ID == "" || text == "" || ids[choice.ID] || texts[text] {
			return false
		}
		ids[choice.ID], texts[text] = true, true
		if choice.Correct {
			correct++
		}
	}
	return correct == 1
}

func solutionConsistent(asset Asset) bool {
	solution := strings.TrimSpace(asset.TeacherPrivate.Solution)
	return solution != "" && (asset.TeacherPrivate.NumericValue != nil || strings.Contains(strings.ToLower(solution), strings.ToLower(asset.TeacherPrivate.Answer)))
}

func (validator Validator) duplicate(asset Asset) bool {
	return validator.Duplicates != nil && validator.Duplicates.Exists(NormalizedPromptHash(asset.PromptPublic), asset.QuestionID)
}

func NormalizedPromptHash(prompt string) string {
	fields := strings.Fields(strings.ToLower(prompt))
	sort.Strings(fields)
	sum := sha256.Sum256([]byte(strings.Join(fields, " ")))
	return hex.EncodeToString(sum[:])
}

func check(name string, passed bool, detail string) Check {
	return Check{Name: name, Passed: passed, Detail: detail}
}
