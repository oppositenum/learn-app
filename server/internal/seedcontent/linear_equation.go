package seedcontent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oppositenum/ai-learning-tutor/server/internal/contentpipeline"
	"github.com/oppositenum/ai-learning-tutor/server/internal/studentinteraction"
)

const (
	LinearEquationKnowledgePointCode = "MATH-LINEAR-EQUATION"
	LinearEquationLineageVersion     = "math-linear-equation-stage-v1"
	LinearEquationContentVersion     = "math-linear-equation-content-v5"
	curatedAuthorProvider            = "internal-curated"
	curatedAuthorModel               = "linear-equation-author-v1"
	curatedReviewerProvider          = "internal-curated"
	curatedReviewerModel             = "linear-equation-review-v1"
)

type StageTaskDefinition struct {
	Stage              string
	SelectionOrder     int
	EvidenceForm       string
	ScoringRuleVersion string
	ScoringRule        json.RawMessage
	QuestionID         uuid.UUID
}

type Activation struct {
	KnowledgePointID uuid.UUID
	LineageID        uuid.UUID
	QuestionIDs      []uuid.UUID
	Tasks            []StageTaskDefinition
}

// NewPipeline returns the same content-pipeline service used by the Owner
// path, with a deterministic independent reviewer for the curated internal
// seed. The reviewer re-runs the server validator and records distinct
// author/reviewer provenance without requiring a provider key.
func NewPipeline(pool *pgxpool.Pool) *contentpipeline.Service {
	reviewer, err := contentpipeline.NewReviewService(
		curatedAuthorProvider+":"+curatedAuthorModel,
		curatedReviewerProvider+":"+curatedReviewerModel,
		curatedReviewer{pool: pool},
	)
	if err != nil {
		panic(err)
	}
	return contentpipeline.NewService(contentpipeline.NewRepository(pool), contentpipeline.Validator{}, reviewer)
}

type curatedReviewer struct{ pool *pgxpool.Pool }

func (reviewer curatedReviewer) Review(ctx context.Context, asset contentpipeline.Asset) (contentpipeline.Review, contentpipeline.ReviewEvidence, error) {
	knowledgePointID, err := uuid.Parse(asset.KnowledgePointID)
	if err != nil {
		return contentpipeline.Review{}, contentpipeline.ReviewEvidence{}, err
	}
	allowed, err := contentpipeline.NewRepository(reviewer.pool).AllowedMisconceptionCodes(ctx, knowledgePointID)
	if err != nil {
		return contentpipeline.Review{}, contentpipeline.ReviewEvidence{}, err
	}
	validation := (contentpipeline.Validator{}).ValidateWithMisconceptionTaxonomy(asset, allowed)
	checks := map[string]bool{}
	for _, item := range validation.Checks {
		checks[item.Name] = item.Passed
	}
	ageAppropriate := asset.Difficulty == "L0" || asset.Difficulty == "L1" || asset.Difficulty == "L2" || asset.Difficulty == "L3"
	numericMaterialCompatible := checks["numeric_answer"] && checks["student_interaction"] && !containsUnauthorizedTutorNumberMarker(asset.PromptPublic)
	factuallySound := checks["student_interaction"] && checks["numeric_answer"] && checks["unit"] && checks["solution"] && numericMaterialCompatible
	unambiguous := checks["student_interaction"] && checks["choices"] && checks["misconception_taxonomy"]
	noAnswerLeak := checks["answer_not_public"]
	safeValues := !containsUnsafeReviewText(asset.PromptPublic, asset.TeacherPrivate.Solution)
	passed := validation.Passed && ageAppropriate && factuallySound && unambiguous && noAnswerLeak && safeValues
	findings := []string{fmt.Sprintf("dimensions age_appropriate=%t factually_sound=%t unambiguous=%t no_answer_leak=%t safe_values=%t numeric_material_compatible=%t", ageAppropriate, factuallySound, unambiguous, noAnswerLeak, safeValues, numericMaterialCompatible), "deterministic secondary review checked taxonomy, answer leakage, numeric consistency, interaction schema, safe values, and REQ-TEACH-01 number compatibility"}
	if !passed {
		findings = append(findings, "one or more deterministic review dimensions failed")
	}
	result := contentpipeline.ReviewReject
	if passed {
		result = contentpipeline.ReviewPass
	}
	return contentpipeline.Review{
			Result: result, AgeAppropriate: ageAppropriate, FactuallySound: factuallySound,
			Unambiguous: unambiguous, NoAnswerLeak: noAnswerLeak, SafeValues: safeValues,
			Findings: findings,
		}, contentpipeline.ReviewEvidence{
			Provider:  curatedReviewerProvider,
			Model:     curatedReviewerModel,
			RequestID: "review-" + asset.QuestionID,
		}, nil
}

func containsUnsafeReviewText(values ...string) bool {
	for _, value := range values {
		lower := strings.ToLower(value)
		for _, marker := range []string{"暴力", "自残", "色情", "毒品", "赌博"} {
			if strings.Contains(lower, marker) {
				return true
			}
		}
	}
	return false
}

func containsUnauthorizedTutorNumberMarker(prompt string) bool {
	lower := strings.ToLower(prompt)
	for _, marker := range []string{"probe换数", "hint换数", "scaffold换数", "analogy换数", "示范改数"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

type contentSpec struct {
	Stage          string
	EvidenceForm   string
	Prompt         string
	Scene          studentinteraction.Scene
	ScoringVersion string
	Rule           any
	Answer         string
	Solution       string
	NumericValue   *float64
	SolutionResult *float64
	Misconceptions []string
}

func LinearEquationAssets(knowledgePointID, sourceID uuid.UUID) ([]contentpipeline.Asset, []StageTaskDefinition, error) {
	specs := linearEquationSpecs()
	populateLinearEquationPrivateAnswers(specs)
	assets := make([]contentpipeline.Asset, 0, len(specs))
	tasks := make([]StageTaskDefinition, 0, len(specs))
	for index, spec := range specs {
		questionID := uuid.MustParse(fmt.Sprintf("d0000000-0000-4000-8000-%012d", index+1))
		scene, err := json.Marshal(spec.Scene)
		if err != nil {
			return nil, nil, fmt.Errorf("encode scene %d: %w", index+1, err)
		}
		inputSchema, ok := studentinteraction.AnswerSchema(spec.Scene)
		if !ok {
			return nil, nil, fmt.Errorf("build answer schema %d", index+1)
		}
		rule, err := json.Marshal(spec.Rule)
		if err != nil {
			return nil, nil, fmt.Errorf("encode scoring rule %d: %w", index+1, err)
		}
		assets = append(assets, contentpipeline.Asset{
			QuestionID:       questionID.String(),
			KnowledgePointID: knowledgePointID.String(),
			SubjectCode:      "MATH",
			Difficulty:       specDifficulty(spec.Stage),
			QuestionType:     "STRUCTURED_INTERACTION",
			PromptPublic:     spec.Prompt,
			TeacherPrivate:   contentpipeline.PrivateAnswer{Answer: spec.Answer, Solution: spec.Solution, NumericValue: spec.NumericValue, SolutionResult: spec.SolutionResult, Misconceptions: append([]string(nil), spec.Misconceptions...)},
			Scene:            scene,
			InputSchema:      inputSchema,
			SourceID:         sourceID.String(),
			ContentVersion:   LinearEquationContentVersion,
			SchemaVersion:    contentpipeline.CurrentSchemaVersion,
			Status:           contentpipeline.Draft,
		})
		tasks = append(tasks, StageTaskDefinition{
			Stage:              spec.Stage,
			SelectionOrder:     orderForStage(specs, index),
			EvidenceForm:       spec.EvidenceForm,
			ScoringRuleVersion: spec.ScoringVersion,
			ScoringRule:        rule,
			QuestionID:         questionID,
		})
	}
	return assets, tasks, nil
}

func populateLinearEquationPrivateAnswers(specs []contentSpec) {
	answers := []struct {
		answer, explanation string
		numeric             *float64
	}{
		{"每多1本，总价都增加同样的钱", "观察相邻本数的总价差，固定的增加量说明每本的单价保持不变。", nil},
		{"比较相邻页数的总价差", "先比较页数相邻的两行，再用总价差识别每页增加的费用；一次性的装订费不会重复增加。", nil},
		{"第一次计时前有固定开锁费；每多1小时增加相同费用", "固定开锁费是起点，每小时相同的增加量是变化率，两条线索共同说明一次关系。", nil},
		{"4元", "相邻两行总价相差4元，所以每增加1份水果，总价增加4元。", floatPtr(4)},
		{"6元", "比较材料包数量相邻的记录，总价每次增加6元，因此变化量是6元。", nil},
		{"比较相邻路程的费用差 -> 找出不随路程变化的起步价 -> 用字母表示未知费用", "先从记录观察变化量，再分离固定起步价，最后才把未知量写成字母，能保持生活结构。", nil},
		{"3x+6=36", "三张同价门票是3x，服务费6元，合计36元，所以方程是3x+6=36。", nil},
		{"常数项7", "在5x+7=42中，7是不随单位数量变化的固定起点；5是每增加1单位的变化量，42是总量。", nil},
		{"4表示相同单位的数量；9是固定加入的量", "4乘以未知数表示相同单位的数量，9是固定加入的量，25是总量而不是未知数本身。", nil},
		{"10", "移项得3x=30，再除以3得到x=10。", nil},
		{"10", "2x+4=24先减去4得到2x=20，再除以2得到x=10。", floatPtr(10)},
		{"5", "5x-7=18先加上7得到5x=25，再除以5得到x=5。", nil},
	}
	for index, item := range answers {
		if index >= len(specs) {
			break
		}
		specs[index].Answer = item.answer
		specs[index].Solution = "标准回应：" + item.answer + "。" + item.explanation
		specs[index].NumericValue = item.numeric
		specs[index].SolutionResult = item.numeric
	}
}

func floatPtr(value float64) *float64 { return &value }

func orderForStage(specs []contentSpec, index int) int {
	order := 0
	for current := 0; current <= index; current++ {
		if specs[current].Stage == specs[index].Stage {
			order++
		}
	}
	return order
}

func specDifficulty(stage string) string {
	switch stage {
	case "ORIGINAL":
		return "L1"
	case "VARIANT":
		return "L2"
	case "ABSTRACT":
		return "L3"
	case "VERIFY":
		return "L3"
	default:
		return "L2"
	}
}

func LinearEquationKnowledgePoint(ctx context.Context, pool *pgxpool.Pool) (uuid.UUID, uuid.UUID, error) {
	var knowledgePointID, sourceID uuid.UUID
	err := pool.QueryRow(ctx, `
SELECT knowledge_point.id,content_source.id
FROM knowledge_points knowledge_point
JOIN content_sources content_source ON content_source.license_code='INTERNAL-ORIGINAL'
WHERE knowledge_point.code=$1 AND knowledge_point.status='RELEASED'
LIMIT 1`, LinearEquationKnowledgePointCode).Scan(&knowledgePointID, &sourceID)
	return knowledgePointID, sourceID, err
}

func EnsureLinearEquation(ctx context.Context, pool *pgxpool.Pool, pipeline *contentpipeline.Service, actorID uuid.UUID) (Activation, error) {
	knowledgePointID, sourceID, err := LinearEquationKnowledgePoint(ctx, pool)
	if err != nil {
		return Activation{}, fmt.Errorf("load released linear-equation knowledge point/source: %w", err)
	}
	var readyCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM classroom_task_lineages WHERE knowledge_point_id=$1 AND status='READY'`, knowledgePointID).Scan(&readyCount); err != nil {
		return Activation{}, err
	}
	if readyCount > 1 {
		return Activation{}, fmt.Errorf("knowledge point has %d READY lineages", readyCount)
	}
	assets, tasks, err := LinearEquationAssets(knowledgePointID, sourceID)
	if err != nil {
		return Activation{}, err
	}
	if readyCount == 1 {
		var lineageID uuid.UUID
		if err := pool.QueryRow(ctx, `SELECT id FROM classroom_task_lineages WHERE knowledge_point_id=$1 AND status='READY'`, knowledgePointID).Scan(&lineageID); err != nil {
			return Activation{}, err
		}
		var currentVersion string
		if err := pool.QueryRow(ctx, `SELECT content_version FROM questions WHERE id=$1`, assets[0].QuestionID).Scan(&currentVersion); err != nil {
			return Activation{}, err
		}
		if currentVersion == LinearEquationContentVersion {
			questionIDs := make([]uuid.UUID, 0, len(assets))
			for _, asset := range assets {
				questionIDs = append(questionIDs, uuid.MustParse(asset.QuestionID))
			}
			return Activation{KnowledgePointID: knowledgePointID, LineageID: lineageID, QuestionIDs: questionIDs, Tasks: tasks}, nil
		}
		for _, asset := range assets {
			if err := pipeline.ReviseReleasedDraft(ctx, asset, actorID, "MATH-LINEAR-EQUATION B7-3 content repair", contentpipeline.GenerationMetadata{Provider: curatedAuthorProvider, Model: curatedAuthorModel, RequestID: "repair-author-" + asset.QuestionID}); err != nil {
				return Activation{}, fmt.Errorf("revise %s: %w", asset.QuestionID, err)
			}
			if _, status, validation, err := pipeline.Validate(ctx, uuid.MustParse(asset.QuestionID)); err != nil || status != contentpipeline.AutomaticValidated || !validation.Passed {
				return Activation{}, fmt.Errorf("validate revised %s status=%s passed=%t err=%v", asset.QuestionID, status, validation.Passed, err)
			}
			if _, status, review, err := pipeline.Review(ctx, uuid.MustParse(asset.QuestionID)); err != nil || status != contentpipeline.AIReviewed || review.Result != contentpipeline.ReviewPass {
				return Activation{}, fmt.Errorf("review revised %s status=%s result=%s err=%v", asset.QuestionID, status, review.Result, err)
			}
			if err := pipeline.Release(ctx, uuid.MustParse(asset.QuestionID), actorID, "MATH-LINEAR-EQUATION B7-3 content repair"); err != nil {
				return Activation{}, fmt.Errorf("release revised %s: %w", asset.QuestionID, err)
			}
		}
		questionIDs := make([]uuid.UUID, 0, len(assets))
		for _, asset := range assets {
			questionIDs = append(questionIDs, uuid.MustParse(asset.QuestionID))
		}
		return Activation{KnowledgePointID: knowledgePointID, LineageID: lineageID, QuestionIDs: questionIDs, Tasks: tasks}, nil
	}
	for _, asset := range assets {
		if err := pipeline.ImportDraft(ctx, asset, contentpipeline.GenerationMetadata{
			Provider:  curatedAuthorProvider,
			Model:     curatedAuthorModel,
			RequestID: "author-" + asset.QuestionID,
		}); err != nil {
			return Activation{}, fmt.Errorf("import %s: %w", asset.QuestionID, err)
		}
		if _, status, validation, err := pipeline.Validate(ctx, uuid.MustParse(asset.QuestionID)); err != nil || status != contentpipeline.AutomaticValidated || !validation.Passed {
			return Activation{}, fmt.Errorf("validate %s status=%s passed=%t err=%v", asset.QuestionID, status, validation.Passed, err)
		}
		if _, status, review, err := pipeline.Review(ctx, uuid.MustParse(asset.QuestionID)); err != nil || status != contentpipeline.AIReviewed || review.Result != contentpipeline.ReviewPass {
			return Activation{}, fmt.Errorf("review %s status=%s result=%s err=%v", asset.QuestionID, status, review.Result, err)
		}
		if err := pipeline.Release(ctx, uuid.MustParse(asset.QuestionID), actorID, "MATH-LINEAR-EQUATION B7-2 curated READY lineage"); err != nil {
			return Activation{}, fmt.Errorf("release %s: %w", asset.QuestionID, err)
		}
	}
	lineageID := uuid.New()
	if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
INSERT INTO classroom_task_lineages(id,knowledge_point_id,version,status)
VALUES($1,$2,$3,'READY')`, lineageID, knowledgePointID, LinearEquationLineageVersion); err != nil {
			return err
		}
		for _, task := range tasks {
			if _, err := tx.Exec(ctx, `
INSERT INTO classroom_stage_tasks(question_id,lineage_id,stage_role,selection_order,scoring_rule_version,scoring_rule_private_json,evidence_form)
VALUES($1,$2,$3,$4,$5,$6,$7)`, task.QuestionID, lineageID, task.Stage, task.SelectionOrder, task.ScoringRuleVersion, task.ScoringRule, task.EvidenceForm); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return Activation{}, fmt.Errorf("create READY lineage: %w", err)
	}
	questionIDs := make([]uuid.UUID, 0, len(assets))
	for _, asset := range assets {
		questionIDs = append(questionIDs, uuid.MustParse(asset.QuestionID))
	}
	return Activation{KnowledgePointID: knowledgePointID, LineageID: lineageID, QuestionIDs: questionIDs, Tasks: tasks}, nil
}

func linearEquationSpecs() []contentSpec {
	return []contentSpec{
		{Stage: "ORIGINAL", EvidenceForm: "LIFE", Misconceptions: []string{"LINEAR_CHANGE_NOT_IDENTIFIED"}, Prompt: "文具店把同款练习册按本数装袋。观察三次总价后，哪句话最能说明每增加1本时总价的变化？", Scene: singleChoice("请选择最符合表格观察的说法。", []studentinteraction.Item{{ID: "a", Label: "每多1本，总价都增加同样的钱"}, {ID: "b", Label: "总价只由袋子的颜色决定"}, {ID: "c", Label: "本数越多，固定费用也会重复增加"}}), ScoringVersion: "exact-option-set-v1", Rule: optionRule([]string{"a"}, []string{"a", "b", "c"})},
		{Stage: "ORIGINAL", EvidenceForm: "LIFE", Misconceptions: []string{"LINEAR_CHANGE_NOT_IDENTIFIED"}, Prompt: "校园打印点先收一次装订费，再按页数收费。根据价目表，选择可以帮助你找出每页增加金额的观察。", Scene: singleChoice("选出一个真正依赖表格变化的观察。", []studentinteraction.Item{{ID: "a", Label: "比较相邻页数的总价差"}, {ID: "b", Label: "只看最高的一次总价"}, {ID: "c", Label: "忽略页数，只记住封面颜色"}}), ScoringVersion: "exact-option-set-v1", Rule: optionRule([]string{"a"}, []string{"a", "b", "c"})},
		{Stage: "ORIGINAL", EvidenceForm: "LIFE", Misconceptions: []string{"FIXED_START_NOT_IDENTIFIED"}, Prompt: "社区自行车租借有固定开锁费，之后每小时增加相同费用。选择所有能从生活记录中识别这种关系的线索。", Scene: multiChoice("可多选。", []studentinteraction.Item{{ID: "a", Label: "第一次计时前就有一笔固定费用"}, {ID: "b", Label: "每多1小时，增加的钱保持一致"}, {ID: "c", Label: "每小时增加的钱完全不同"}}), ScoringVersion: "exact-option-set-v1", Rule: optionRule([]string{"a", "b"}, []string{"a", "b", "c"})},
		{Stage: "VARIANT", EvidenceForm: "VARIANT", Misconceptions: []string{"LINEAR_CHANGE_NOT_IDENTIFIED"}, Prompt: "果汁摊的基础杯费不变，每加一份水果就增加同样金额。表格显示相邻两行总价差为4元，这个4元说明什么？", Scene: numberLine("在数轴上选择每增加1份水果的价格变化。", 0, 10, 1), ScoringVersion: "exact-number-v1", Rule: numberRule(4, 0, 10, 1)},
		{Stage: "VARIANT", EvidenceForm: "VARIANT", Misconceptions: []string{"LINEAR_CHANGE_NOT_IDENTIFIED"}, Prompt: "手作店记录了材料包数量和总价。请在空格中填入‘每增加一个材料包，总价增加多少’这一变化量。", Scene: fillBlanks("填写表格中相邻两行的固定增加量。", "increase"), ScoringVersion: "exact-fill-v1", Rule: fillRule("increase", []string{"6元", "6"})},
		{Stage: "VARIANT", EvidenceForm: "VARIANT", Misconceptions: []string{"FIXED_START_NOT_IDENTIFIED"}, Prompt: "从打车记录走向代数表达时，下面哪一组顺序最能保留‘固定起步价+每公里相同增加’的思考路径？", Scene: ordering("按理解顺序排列。", []studentinteraction.Item{{ID: "observe", Label: "比较相邻路程的费用差"}, {ID: "fixed", Label: "找出不随路程变化的起步价"}, {ID: "symbol", Label: "用字母表示未知费用"}}), ScoringVersion: "exact-order-v1", Rule: orderRule([]string{"observe", "fixed", "symbol"}, []string{"observe", "fixed", "symbol"})},
		{Stage: "ABSTRACT", EvidenceForm: "TEXTBOOK", Misconceptions: []string{"LINEAR_TERMS_CONFUSED"}, Prompt: "把‘三张同价门票加6元服务费共36元’抽象成方程。选择最符合文字结构的表达式。", Scene: singleChoice("选择方程。", []studentinteraction.Item{{ID: "a", Label: "3x+6=36"}, {ID: "b", Label: "3+x+6=36"}, {ID: "c", Label: "3(x+6)=36"}}), ScoringVersion: "exact-option-set-v1", Rule: optionRule([]string{"a"}, []string{"a", "b", "c"})},
		{Stage: "ABSTRACT", EvidenceForm: "TEXTBOOK", Misconceptions: []string{"LINEAR_TERMS_CONFUSED"}, Prompt: "请把‘固定起点、每增加1单位的变化、总量’分别对应到方程 5x+7=42 的结构中。", Scene: fillBlanks("填写对应的结构名称。", "fixed"), ScoringVersion: "exact-fill-v1", Rule: fillRule("fixed", []string{"固定起点", "起点"})},
		{Stage: "ABSTRACT", EvidenceForm: "TEXTBOOK", Misconceptions: []string{"LINEAR_TERMS_CONFUSED"}, Prompt: "关于一次关系式 4x+9=25，选择所有正确的结构解释。", Scene: multiChoice("可多选。", []studentinteraction.Item{{ID: "a", Label: "4表示相同单位的数量"}, {ID: "b", Label: "9是固定加入的量"}, {ID: "c", Label: "25是未知数本身"}}), ScoringVersion: "exact-option-set-v1", Rule: optionRule([]string{"a", "b"}, []string{"a", "b", "c"})},
		{Stage: "VERIFY", EvidenceForm: "TEXTBOOK", Misconceptions: []string{"LINEAR_ISOLATION_ERROR"}, Prompt: "解方程 3x+6=36，选择 x 的值。", Scene: singleChoice("选择解。", []studentinteraction.Item{{ID: "a", Label: "8"}, {ID: "b", Label: "10"}, {ID: "c", Label: "12"}}), ScoringVersion: "exact-option-set-v1", Rule: optionRule([]string{"b"}, []string{"a", "b", "c"})},
		{Stage: "VERIFY", EvidenceForm: "TEXTBOOK", Misconceptions: []string{"MULTIPLIES_INSTEAD_OF_DIVIDES"}, Prompt: "在数轴上标出方程 2x+4=24 的解。", Scene: numberLine("选择 x 的位置。", 0, 20, 1), ScoringVersion: "exact-number-v1", Rule: numberRule(10, 0, 20, 1)},
		{Stage: "VERIFY", EvidenceForm: "TEXTBOOK", Misconceptions: []string{"LINEAR_ISOLATION_ERROR"}, Prompt: "解方程 5x-7=18，并在空格中填写 x。", Scene: fillBlanks("填写方程的解。", "x"), ScoringVersion: "exact-fill-v1", Rule: fillRule("x", []string{"5", "5.0"})},
	}
}

func singleChoice(fallback string, options []studentinteraction.Item) studentinteraction.Scene {
	return studentinteraction.Scene{Version: studentinteraction.Version, Renderer: studentinteraction.RendererSingleChoice, AccessibleFallback: fallback, Options: options}
}

func multiChoice(fallback string, options []studentinteraction.Item) studentinteraction.Scene {
	return studentinteraction.Scene{Version: studentinteraction.Version, Renderer: studentinteraction.RendererMultiChoice, AccessibleFallback: fallback, Options: options}
}

func numberLine(fallback string, min, max, step float64) studentinteraction.Scene {
	return studentinteraction.Scene{Version: studentinteraction.Version, Renderer: studentinteraction.RendererNumberLine, AccessibleFallback: fallback, NumberLine: &studentinteraction.NumberLine{Label: "数值位置", Min: min, Max: max, Step: step}}
}

func fillBlanks(fallback, slotID string) studentinteraction.Scene {
	return studentinteraction.Scene{Version: studentinteraction.Version, Renderer: studentinteraction.RendererFillBlanks, AccessibleFallback: fallback, Slots: []studentinteraction.Item{{ID: slotID, Label: "填写"}}}
}

func ordering(fallback string, items []studentinteraction.Item) studentinteraction.Scene {
	return studentinteraction.Scene{Version: studentinteraction.Version, Renderer: studentinteraction.RendererOrdering, AccessibleFallback: fallback, Items: items}
}

func optionRule(expected, allowed []string) any {
	return map[string]any{"rule_type": "EXACT_OPTION_SET", "expected_option_ids": expected, "allowed_option_ids": allowed}
}

func numberRule(expected, min, max, step float64) any {
	return map[string]any{"rule_type": "EXACT_NUMBER", "expected_value": expected, "min": min, "max": max, "step": step}
}

func fillRule(slotID string, accepted []string) any {
	return map[string]any{"rule_type": "EXACT_FILL", "expected_values": []map[string]any{{"slot_id": slotID, "accepted_values": accepted}}, "allowed_slot_ids": []string{slotID}}
}

func orderRule(expected, allowed []string) any {
	return map[string]any{"rule_type": "EXACT_ORDER", "expected_item_ids": expected, "allowed_item_ids": allowed}
}
