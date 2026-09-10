package safety

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

const PolicyVersion = "minor-safety-v1"

type Category string

const (
	CategoryOffTopicLong        Category = "OFF_TOPIC_LONG"
	CategoryPersonalInformation Category = "PERSONAL_INFORMATION"
	CategoryFamilyPrivacy       Category = "FAMILY_PRIVACY"
	CategoryDangerousExperiment Category = "DANGEROUS_EXPERIMENT"
	CategoryHealth              Category = "HEALTH"
	CategorySelfHarm            Category = "SELF_HARM"
	CategoryBullying            Category = "BULLYING"
	CategorySexualContent       Category = "SEXUAL_CONTENT"
)

type Severity string

const (
	SeverityLow      Severity = "LOW"
	SeverityModerate Severity = "MODERATE"
	SeverityHigh     Severity = "HIGH"
	SeverityCritical Severity = "CRITICAL"
)

type Action string

const (
	ActionReturnToLearning Action = "RETURN_TO_LEARNING"
	ActionProtectPrivacy   Action = "PROTECT_PRIVACY"
	ActionTalkToAdult      Action = "TALK_TO_TRUSTED_ADULT"
	ActionStopExperiment   Action = "STOP_EXPERIMENT_AND_GET_ADULT"
	ActionSeekHealthHelp   Action = "SEEK_HEALTH_HELP"
	ActionUrgentHelp       Action = "SEEK_URGENT_HELP"
	ActionReportBullying   Action = "REPORT_BULLYING"
	ActionProtectBody      Action = "PROTECT_BODY_AND_GET_ADULT"
)

type Classification struct {
	Matched          bool
	Category         Category
	Severity         Severity
	Action           Action
	StudentMessage   string
	EscalateToParent bool
}

var (
	emailPattern = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)
	phonePattern = regexp.MustCompile(`(?:^|[^0-9])1[3-9][0-9]{9}(?:$|[^0-9])`)
)

// Classify applies a deliberately narrow deterministic policy before a child
// message can be sent to the Teaching Agent. It is a safety gate, not a claim
// that every unsafe expression can be recognized without a dedicated model.
func Classify(input string) Classification {
	value := strings.ToLower(strings.TrimSpace(input))
	if value == "" {
		return Classification{}
	}

	var category Category
	switch {
	case containsAny(value, "不想活", "想自杀", "要自杀", "伤害自己", "割腕", "跳楼", "杀了他", "杀了她", "hurt myself", "kill myself", "suicide"):
		category = CategorySelfHarm
	case containsAny(value, "性侵", "猥亵", "私密部位", "裸体照片", "不雅照", "裸照", "sexual assault", "nude photo"):
		category = CategorySexualContent
	case containsAny(value, "自制火药", "做炸弹", "制造炸弹", "点燃酒精", "混合漂白", "氯气", "制毒", "爆炸实验", "make a bomb"):
		category = CategoryDangerousExperiment
	case containsAny(value, "被欺负", "校园欺凌", "被霸凌", "有人打我", "有人威胁我", "被勒索", "bullying", "bullied"):
		category = CategoryBullying
	case containsAny(value, "胸痛", "呼吸困难", "药物过量", "吃多少药", "大量出血", "昏迷", "发高烧", "严重肚子痛", "overdose", "can't breathe"):
		category = CategoryHealth
	case containsAny(value, "爸妈吵架", "父母吵架", "爸爸打妈妈", "妈妈打爸爸", "父母离婚", "家里没钱", "不让家里知道", "家庭秘密"):
		category = CategoryFamilyPrivacy
	case emailPattern.MatchString(value), phonePattern.MatchString(value), containsAny(value, "我的手机号", "电话号码是", "我的地址", "我住在", "身份证号", "家庭住址", "微信号是", "qq号是"):
		category = CategoryPersonalInformation
	case utf8.RuneCountInString(value) >= 120 && containsAny(value, "陪我聊天", "讲个故事", "游戏攻略", "明星八卦", "你喜欢什么", "聊一会儿"):
		category = CategoryOffTopicLong
	default:
		return Classification{}
	}
	classification, _ := FixedClassification(PolicyVersion, category)
	return classification
}

// FixedClassification reconstructs the bounded response for an already
// persisted classification without retaining the child's input.
func FixedClassification(policyVersion string, category Category) (Classification, bool) {
	if policyVersion != PolicyVersion {
		return Classification{}, false
	}
	var classification Classification
	switch category {
	case CategorySelfHarm:
		classification = classified(category, SeverityCritical, ActionUrgentHelp, true, "这关系到你或他人的安全。请立刻停下来，去找身边可信任的大人；如果有人正处在危险中，请马上联系当地紧急求助。为了保护你，我会把这条安全提醒告诉家长。")
	case CategorySexualContent:
		classification = classified(category, SeverityHigh, ActionProtectBody, true, "这涉及身体和隐私安全。不要发送图片或更多个人细节，请尽快告诉可信任的大人。为了保护你，我会把安全类别告诉家长，但不会转发你写的原话。")
	case CategoryDangerousExperiment:
		classification = classified(category, SeverityHigh, ActionStopExperiment, true, "这个操作可能造成中毒、燃烧或爆炸。请不要尝试，先离开材料并找可信任的大人处理。为了保护你，我会把安全类别告诉家长。")
	case CategoryBullying:
		classification = classified(category, SeverityHigh, ActionReportBullying, true, "被欺负不是你的错。先去安全的地方，并把情况告诉可信任的家长或老师。为了帮助你得到支持，我会把安全类别告诉家长，但不会转发你的原话。")
	case CategoryHealth:
		classification = classified(category, SeverityModerate, ActionSeekHealthHelp, false, "健康问题需要由可信任的大人和专业人员判断。请先告诉身边的大人；如果症状严重或正在变坏，请及时联系当地医疗急救。不要只依赖学习助手的回答。")
	case CategoryFamilyPrivacy:
		classification = classified(category, SeverityModerate, ActionTalkToAdult, false, "你不用在这里写更多家庭隐私。可以先找一位你信任的大人或老师面对面说；我们也可以回到当前学习任务。")
	case CategoryPersonalInformation:
		classification = classified(category, SeverityModerate, ActionProtectPrivacy, false, "请不要在这里发送姓名、电话、地址、账号或证件号码。你可以删去这些信息，然后继续回答学习问题。")
	case CategoryOffTopicLong:
		classification = classified(category, SeverityLow, ActionReturnToLearning, false, "我们先不继续这段长聊天，回到当前学习任务。你可以用一句话告诉我题目中最困惑的地方。")
	default:
		return Classification{}, false
	}
	return classification, true
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func classified(category Category, severity Severity, action Action, escalate bool, message string) Classification {
	return Classification{Matched: true, Category: category, Severity: severity, Action: action, StudentMessage: message, EscalateToParent: escalate}
}
