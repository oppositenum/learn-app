import { flushPromises, mount } from '@vue/test-utils'

import StudentGrowthPage from './StudentGrowthPage.vue'

it('renders growth-base counts from the Student growth response', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => ({
    ok: true,
    json: async () => ({
      total_energy: 42,
      streak_days: 7,
      buildings: {
        completed_days: 7,
        completed_sessions: 9,
        mastered_knowledge_points: 6,
        mastered_by_subject: { MATH: 2, CHINESE: 1, ENGLISH: 1, PHYSICS: 1, CHEMISTRY: 1 },
        cross_subject_insights: 3,
      },
    }),
  } as Response)))

  const wrapper = mount(StudentGrowthPage)
  await flushPromises()

  expect(wrapper.text()).toContain('42')
  expect(wrapper.text()).toContain('连续 7 天')
  expect(wrapper.text()).toContain('2 个数学知识点已点亮')
  expect(wrapper.text()).toContain('2 个语言知识点已点亮')
  expect(wrapper.text()).toContain('2 个科学知识点 · 3 次跨学科发现')
  expect(wrapper.text()).not.toContain('12 个知识点')
})
