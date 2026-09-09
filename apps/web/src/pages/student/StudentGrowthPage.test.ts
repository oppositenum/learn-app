import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'

import { useAuthSession } from '../../stores/auth'
import StudentGrowthPage from './StudentGrowthPage.vue'

it('renders growth-base counts from the Student growth response', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => ({
    ok: true,
    json: async () => ({
      student_id: 'student-1',
      learning_date: '2026-08-26',
      total_energy: 42,
      streak_days: 7,
      buildings: {
        completed_days: 7,
        completed_sessions: 9,
        mastered_knowledge_points: 6,
        mastered_by_subject: { MATH: 2, CHINESE: 1, ENGLISH: 1, PHYSICS: 1, CHEMISTRY: 1 },
        cross_subject_insights: 3,
      },
      growth_evidence: {
        policy_version: 'growth-evidence-v1',
        indicators: [
          { code: 'INDEPENDENT_SOLVING', label: '独立解决', count: 8, events: [{ source_kind: 'CLASSROOM_STAGE_EVIDENCE', source_id: 'event-1', subject: 'MATH', knowledge_point: '一元一次方程', occurred_at: '2026-08-26T10:00:00Z' }], events_truncated: false },
          { code: 'UNDERSTANDING_AFTER_HELP', label: '帮助后理解', count: 2, events: [], events_truncated: false },
          { code: 'SELF_CORRECTION', label: '自我纠正', count: 3, events: [], events_truncated: false },
          { code: 'TRANSFER_SUCCESS', label: '迁移成功', count: 4, events: [], events_truncated: false },
          { code: 'DELAYED_REVIEW', label: '延迟复习', count: 1, events: [], events_truncated: false },
        ],
      },
    }),
  } as Response)))

  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthSession().user.value = { user_id: 'user-1', role: 'STUDENT', display_name: '学生', student_id: 'student-1' }
  const wrapper = mount(StudentGrowthPage, { global: { plugins: [pinia] } })
  await flushPromises()

  expect(wrapper.text()).toContain('独立解决')
  expect(wrapper.text()).toContain('帮助后理解')
  expect(wrapper.text()).toContain('自我纠正')
  expect(wrapper.text()).toContain('迁移成功')
  expect(wrapper.text()).toContain('延迟复习')
  expect(wrapper.text()).toContain('兼容能量记录：42')
  expect(wrapper.text()).toContain('连续 7 天')
  expect(wrapper.text()).toContain('2 个数学知识点已点亮')
  expect(wrapper.text()).toContain('2 个语言知识点已点亮')
  expect(wrapper.text()).toContain('2 个科学知识点 · 3 次跨学科发现')
  expect(wrapper.text()).not.toContain('12 个知识点')
})
