import { flushPromises, mount } from '@vue/test-utils'

import AdminCostPage from './AdminCostPage.vue'

it('renders cost and learning-effect metrics as computed values', async () => {
  vi.stubGlobal('fetch', vi.fn(async (input: string | URL | Request) => {
    if (String(input).includes('/learning-effects')) return {
      ok: true,
      json: async () => ({
        definitions: {},
        first_answer_effective_latency: { sessions: 4, measured_sessions: 3, average_ms: 12500 },
        student_interaction_share: { student_events: 6, tutor_events: 4, rate: 0.6 },
        completion_by_assistance: [{ assistance_level: 0, completed: 3, exits: 4, rate: 0.75 }],
        transfer_accuracy: { correct: 2, attempts: 3, rate: 2 / 3 },
        retention: [{ days: 1, correct: 1, attempts: 2, rate: 0.5 }, { days: 7, correct: 1, attempts: 1, rate: 1 }],
        dont_know_reengagement: { requests: 2, reengaged: 1, rate: 0.5 },
        exit_stages: [{ stage: 'COMPLETE', outcome: 'COMPLETED', count: 3 }],
        ai_anomaly: { requests: 10, anomalies: 1, rate: 0.1 },
      }),
    } as Response
    return {
      ok: true,
      json: async () => ({
      records: [],
      summary: {
        total_cost_usd: '1',
        cost_per_active_student_day_usd: '0.5',
        cost_per_20_minute_lesson_usd: '0.25',
        cost_per_mastered_skill_usd: '0.125',
        cached_ratio: '0.2',
        stt_cost_usd: '0.01',
        tts_cost_usd: '0.02',
        strong_model_ratio: '0.1',
        average_tokens_per_request: '100',
      },
    }),
    } as Response
  }))

  const wrapper = mount(AdminCostPage)
  await flushPromises()

  expect(wrapper.text()).toContain('20 分钟 $0.2500')
  expect(wrapper.text()).toContain('掌握知识点 $0.1250')
  expect(wrapper.text()).toContain('STT $0.0100')
  expect(wrapper.text()).toContain('TTS $0.0200')
  expect(wrapper.text()).toContain('学生交互事件占比')
  expect(wrapper.text()).toContain('60.0%')
  expect(wrapper.text()).toContain('D+7 保持率')
  expect(wrapper.text()).toContain('结构化 AI 异常率')
  expect(wrapper.text()).not.toContain('${')
})
