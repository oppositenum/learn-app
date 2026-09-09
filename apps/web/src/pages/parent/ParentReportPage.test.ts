import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'

import ParentReportPage from './ParentReportPage.vue'
import ParentSessionReportPage from './ParentSessionReportPage.vue'

const children = [
  { student_id: 'student-1', display_name: '小航', grade_level: 7, active_session_id: null, subject: null, knowledge_point: null, started_at: null },
]

const report = {
  student_id: 'student-1',
  summary: { completed_sessions: 1, active_seconds: 7200, total_energy: 2, streak_days: 1, reward_events: 1 },
  activity_days: [{ date: '2026-08-27', completed_sessions: 1, active_seconds: 7200 }],
  recent_sessions: [{ id: 'session-1', subject: '化学', knowledge_point: '物理变化与化学变化', status: 'COMPLETED', state: 'COMPLETE', started_at: '2026-08-27T00:00:00Z', ended_at: '2026-08-27T02:00:00Z', active_seconds: 7200 }],
  growth_evidence: {
    policy_version: 'growth-evidence-v1',
    indicators: [
      { code: 'INDEPENDENT_SOLVING', label: '独立解决', count: 1, events: [{ source_kind: 'MASTERY_EVIDENCE_PROVENANCE', source_id: 'evidence-1', subject: 'CHEMISTRY', knowledge_point: '物理变化与化学变化', occurred_at: '2026-08-27T02:00:00Z' }], events_truncated: false },
      { code: 'UNDERSTANDING_AFTER_HELP', label: '帮助后理解', count: 0, events: [], events_truncated: false },
      { code: 'SELF_CORRECTION', label: '自我纠正', count: 0, events: [], events_truncated: false },
      { code: 'TRANSFER_SUCCESS', label: '迁移成功', count: 0, events: [], events_truncated: false },
      { code: 'DELAYED_REVIEW', label: '延迟复习', count: 0, events: [], events_truncated: false },
    ],
  },
}

const liveSession = {
  session_id: 'session-1', student_id: 'student-1', subject: '化学', knowledge_point: '物理变化与化学变化',
  started_at: '2026-08-27T00:00:00Z', status: 'COMPLETED', current_state: 'COMPLETE', socratic_round: 0,
  engagement: 'NORMAL', question_prompt: '哪个过程生成了新物质？', correct_answer: { value: '铁钉生锈' },
  full_solution: '铁钉生锈后产生了氧化铁。', detail_mode: 'REPORT', student_answer_visibility: 'WITHHELD_NOT_ACTIVE', answer_correct: true, error_type: '',
  misconceptions: [], tutor_action: 'COMPLETE', tutor_reason: 'rules engine verified spaced mastery', target_minutes: 10,
  active_seconds: 7200, mastery_state: 'LEARNING', mastery_score: 64,
  timeline: [{ sequence: 1, actor: 'TUTOR', action: 'ASK', message: '先找变化后的物质。', at: '2026-08-27T00:00:00Z' }],
}

function testRouter(path: string) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/parent/report', component: ParentReportPage },
      { path: '/parent/report/session/:id', component: ParentSessionReportPage },
    ],
  })
  return router.push(path).then(() => router)
}

afterEach(() => vi.unstubAllGlobals())

it('opens a recent classroom with the selected child context', async () => {
  vi.stubGlobal('fetch', vi.fn(async (input: string | URL | Request) => {
    const path = String(input)
    if (path.includes('/parent/children')) return { ok: true, json: async () => ({ children }) } as Response
    if (path.includes('/safety-events')) return { ok: true, json: async () => ({ events: [] }) } as Response
    return { ok: true, json: async () => report } as Response
  }))
  const router = await testRouter('/parent/report?student=student-1')
  const wrapper = mount(ParentReportPage, { global: { plugins: [router] } })
  await flushPromises()

  const link = wrapper.get('[aria-label="查看化学物理变化与化学变化课堂详情"]')
  expect(link.attributes('href')).toBe('/parent/report/session/session-1?student=student-1')
  await link.trigger('click')
  await flushPromises()
  expect(router.currentRoute.value.fullPath).toBe('/parent/report/session/session-1?student=student-1')
})

it('renders report-safe analysis without a verbatim Student answer', async () => {
  const fetchMock = vi.fn(async (input: string | URL | Request) => {
    const path = String(input)
    if (path.includes('/parent/children')) return { ok: true, json: async () => ({ children }) } as Response
    return { ok: true, json: async () => liveSession } as Response
  })
  vi.stubGlobal('fetch', fetchMock)
  const router = await testRouter('/parent/report/session/session-1?student=student-1')
  const wrapper = mount(ParentSessionReportPage, { global: { plugins: [router] } })
  await flushPromises()

  expect(fetchMock.mock.calls.some(([input]) => String(input).includes('/parent/child/student-1/session/session-1'))).toBe(true)
  expect(wrapper.text()).toContain('铁钉生锈')
  expect(wrapper.text()).toContain('铁钉生锈后产生了氧化铁。')
  expect(wrapper.text()).not.toContain('孩子最后回答')
  expect(wrapper.text()).toContain('120 分钟')
  expect(wrapper.text()).toContain('规则引擎已核验独立证据与跨天复习证据。')
})
