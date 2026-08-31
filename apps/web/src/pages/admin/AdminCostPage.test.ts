import { flushPromises, mount } from '@vue/test-utils'

import AdminCostPage from './AdminCostPage.vue'

it('renders normalized cost metrics as values instead of template source', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => ({
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
  } as Response)))

  const wrapper = mount(AdminCostPage)
  await flushPromises()

  expect(wrapper.text()).toContain('20 分钟 $0.2500')
  expect(wrapper.text()).toContain('掌握知识点 $0.1250')
  expect(wrapper.text()).toContain('STT $0.0100')
  expect(wrapper.text()).toContain('TTS $0.0200')
  expect(wrapper.text()).not.toContain('${')
})
