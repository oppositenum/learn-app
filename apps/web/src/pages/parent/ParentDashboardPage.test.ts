import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { reactive } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'

import type { ParentOverview } from '../../api/parent'
import ParentDashboardPage from './ParentDashboardPage.vue'

// The realtime connection is shared with the live page; here it only names the
// child and reports new classroom messages.
const learning = reactive({
  studentID: 'student-1', sessionID: '', sessionActive: false, connected: true, reconnecting: false,
  childName: '小航', children: [{ student_id: 'student-1', display_name: '小航', active_session_id: null }],
  timeline: [] as unknown[],
  selectChild: vi.fn(),
})
vi.mock('../../features/supervision/useParentLiveConnection', () => ({ useParentLiveConnection: () => learning }))

const activeSession = {
  session_id: 'session-1', status: 'ACTIVE' as const, subject: '数学', knowledge_point: '一元一次方程',
  active_seconds: 125, target_minutes: 20, socratic_round: 2, tutor_action: 'PROBE',
  error_type: 'FIXED_COST_IGNORED', misconceptions: ['EQ-WORD-FIXED-COST'],
}
const plan = {
  plan_id: 'plan-1', date: '2026-09-27', target_minutes: 45,
  blocks: [
    { id: 'block-1', sequence: 1, subject: '数学', knowledge_point: '有理数', minutes: 10, status: 'COMPLETED', progress: 'COMPLETED' as const, session_id: 'session-0' },
    { id: 'block-2', sequence: 2, subject: '数学', knowledge_point: '一元一次方程', minutes: 20, status: 'ACTIVE', progress: 'IN_PROGRESS' as const, session_id: 'session-1' },
    { id: 'block-3', sequence: 3, subject: '物理', knowledge_point: '', minutes: 15, status: 'AVAILABLE', progress: 'NOT_STARTED' as const, session_id: null },
  ],
}

function overview(partial: Partial<ParentOverview>): ParentOverview {
  return { student_id: 'student-1', learning_date: '2026-09-27', session: null, today_plan: null, ...partial }
}

async function mountDashboard(body: ParentOverview) {
  const fetchMock = vi.fn(async () => ({ ok: true, json: async () => body }) as Response)
  vi.stubGlobal('fetch', fetchMock)
  const router = createRouter({ history: createMemoryHistory(), routes: [
    { path: '/parent', component: ParentDashboardPage },
    { path: '/parent/live', component: { template: '<div />' } },
    { path: '/parent/settings', component: { template: '<div />' } },
  ] })
  await router.push('/parent')
  const wrapper = mount(ParentDashboardPage, { global: { plugins: [router] } })
  await flushPromises()
  return { wrapper, fetchMock }
}

enableAutoUnmount(afterEach)

afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

function expectNoAnswersOrEncouragement(html: string) {
  for (const phrase of ['孩子刚才回答', '标准答案', '发送鼓励', '鼓励']) expect(html).not.toContain(phrase)
  expect(html).not.toMatch(/\btext-(xs|sm)\b/)
}

it('shows the active classroom with its round, time and today\'s plan', async () => {
  const { wrapper, fetchMock } = await mountDashboard(overview({ session: activeSession, today_plan: plan }))

  expect(fetchMock).toHaveBeenCalledWith('/api/v1/parent/child/student-1/overview', { credentials: 'same-origin' })
  expect(wrapper.get('[data-testid="parent-session-status"]').text()).toBe('正在学习')
  const card = wrapper.get('[data-testid="parent-session-card"]')
  expect(card.attributes('data-status')).toBe('ACTIVE')
  expect(card.get('h2').text()).toBe('数学 · 一元一次方程')
  expect(wrapper.get('[data-testid="parent-elapsed"]').text()).toBe('2 分 05 秒')
  expect(wrapper.get('[data-testid="parent-target"]').text()).toBe('目标 20 分钟')
  expect(wrapper.get('[data-testid="parent-round"]').text()).toBe('正在第 2 轮启发 · PROBE')
  expect(wrapper.get('[data-testid="parent-action"]').text()).toBe('PROBE')
  expect(wrapper.get('[data-testid="parent-judgement"]').text()).toBe('FIXED_COST_IGNORED')
  expect(wrapper.text()).toContain('卡在哪里')
  expect(wrapper.get('[data-testid="parent-live-link"]').attributes('href')).toBe('/parent/live?student=student-1&session=session-1')
  expect(wrapper.find('[data-testid="parent-waiting"]').exists()).toBe(false)

  const blocks = wrapper.findAll('[data-testid="parent-plan-block"]')
  expect(blocks.map((block) => block.attributes('data-progress'))).toEqual(['COMPLETED', 'IN_PROGRESS', 'NOT_STARTED'])
  expect(blocks.map((block) => block.text())).toEqual([
    '数学 · 有理数10 分钟 已完成',
    '数学 · 一元一次方程20 分钟 进行中',
    '物理15 分钟 未开始',
  ])
  expect(wrapper.find('[data-testid="parent-plan-empty"]').exists()).toBe(false)
  expect(wrapper.get('[aria-label="调整今日计划"]').attributes('href')).toBe('/parent/settings?student=student-1')
  expectNoAnswersOrEncouragement(wrapper.html())
})

it('counts the time of an active classroom and holds it once paused', async () => {
  vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval', 'performance'] })
  const { wrapper } = await mountDashboard(overview({ session: activeSession }))
  await vi.advanceTimersByTimeAsync(3000)
  expect(wrapper.get('[data-testid="parent-elapsed"]').text()).toBe('2 分 08 秒')
  wrapper.unmount()

  const paused = await mountDashboard(overview({ session: { ...activeSession, status: 'PAUSED', active_seconds: 190 } }))
  await vi.advanceTimersByTimeAsync(3000)
  expect(paused.wrapper.get('[data-testid="parent-elapsed"]').text()).toBe('3 分 10 秒')
})

it('shows a paused classroom as paused with its subject and time, never as learning', async () => {
  const pausedPlan = { ...plan, blocks: plan.blocks.map((block) => block.id === 'block-2' ? { ...block, status: 'AVAILABLE', progress: 'PAUSED' as const } : block) }
  const { wrapper } = await mountDashboard(overview({ session: { ...activeSession, status: 'PAUSED', active_seconds: 190 }, today_plan: pausedPlan }))

  expect(wrapper.get('[data-testid="parent-session-status"]').text()).toBe('已暂停')
  expect(wrapper.get('[data-testid="parent-session-card"]').attributes('data-status')).toBe('PAUSED')
  expect(wrapper.get('#live-title').text()).toBe('数学 · 一元一次方程')
  expect(wrapper.get('[data-testid="parent-elapsed"]').text()).toBe('3 分 10 秒')
  expect(wrapper.get('[data-testid="parent-round"]').text()).toBe('停在第 2 轮启发 · PROBE')
  expect(wrapper.text()).not.toContain('正在学习')
  expect(wrapper.find('[data-testid="parent-live-link"]').exists()).toBe(false)
  expect(wrapper.findAll('[data-testid="parent-plan-block"]')[1].text()).toContain('已暂停')
  expectNoAnswersOrEncouragement(wrapper.html())
})

it('says there is no judgement yet instead of making one up', async () => {
  const { wrapper } = await mountDashboard(overview({ session: { ...activeSession, error_type: '', misconceptions: [] } }))
  expect(wrapper.get('[data-testid="parent-judgement"]').text()).toBe('还没有判断')
})

it('waits for the child without a plan and without the last question', async () => {
  const { wrapper } = await mountDashboard(overview({}))

  expect(wrapper.get('[data-testid="parent-waiting"]').text()).toContain('等待孩子开始课堂')
  expect(wrapper.find('[data-testid="parent-session-card"]').exists()).toBe(false)
  expect(wrapper.get('[data-testid="parent-session-status"]').text()).toBe('实时已连接')
  expect(wrapper.get('[data-testid="parent-plan-empty"]').text()).toBe('计划由系统根据当天表现生成。可调整学习时长、优先学科与复习强度。')
  expect(wrapper.findAll('[data-testid="parent-plan-block"]')).toHaveLength(0)
  expect(wrapper.find('button').exists()).toBe(false)
  expectNoAnswersOrEncouragement(wrapper.html())
})

it('reads the overview again when the classroom sends a message', async () => {
  const { wrapper, fetchMock } = await mountDashboard(overview({ session: activeSession }))
  expect(fetchMock).toHaveBeenCalledTimes(1)
  learning.timeline.push({})
  await flushPromises()
  expect(fetchMock).toHaveBeenCalledTimes(2)
  wrapper.unmount()
  learning.timeline.splice(0)
})
