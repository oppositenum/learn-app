import type { TutorAction } from '../api/student'

export const tutorActionLabels = {
	INTRO: '准备开始探索',
	ASK: '先说说你的思路',
	WAIT: '等你写下想法',
	ANALYZE: '正在理解你的思路',
	PROBE: '先检查一个关键关系',
	HINT: '给你一个小提示',
	SCAFFOLD: '把问题拆成一小步',
	ANALOGY: '换个熟悉的场景想想',
	BACKTRACK: '先补上需要的基础',
	EXPLAIN: '用相似例子讲一遍',
	VOICE_EXPLAIN: '听一段简短讲解',
	RETURN: '回到原问题验证',
	ORIGINAL: '先独立完成原题',
	VARIANT: '试试一个变化后的问题',
	ABSTRACT: '把刚才的方法说清楚',
	VERIFY: '再独立验证一次',
	REVIEW: '巩固已经学过的内容',
	BREAK: '先停一下，放松片刻',
	COMPLETE: '这次探索已经完成',
} satisfies Record<TutorAction, string>

export const planModeLabels: Record<string, string> = {
	REMEDIATION: '针对练习',
	CURRENT_GRADE: '本学期内容',
	REVIEW: '巩固一下',
	MICRO_BACKTRACK: '补一小步',
}

export const difficultyLabels: Record<string, string> = {
	L0: '从生活观察开始',
	L1: '一步思考',
	L2: '两步思考',
	L3: '基础应用',
	L4: '变化与选择',
	L5: '综合探索',
}
