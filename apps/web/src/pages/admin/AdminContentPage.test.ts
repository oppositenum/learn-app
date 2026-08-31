import { flushPromises, mount } from '@vue/test-utils'

import AdminContentPage from './AdminContentPage.vue'

const generationOptions = {
  generator_available: true,
  knowledge_points: [
    {
      id: 'kp-math-equation',
      subject_code: 'MATH',
      subject_name: '数学',
      knowledge_point_code: 'MATH-LINEAR-EQUATION',
      name: '一元一次方程',
      description: '从总量与固定量建立未知数关系',
      grade_band_code: 'JUNIOR_SECONDARY',
      grade_band_name: '初中',
      domain_code: 'MATH_JUN_EQUATIONS_INEQUALITIES',
      domain_name: '方程与不等式',
      unit_code: 'JUNIOR_SECONDARY_EQUATIONS_INEQUALITIES',
      unit_name: '方程与不等式',
      default_difficulty: 'L2',
      curriculum_source_name: '互动式学习 V1 产品课程骨架',
      curriculum_source_ref: 'section-4.2/math/junior/一元一次方程',
    },
    {
      id: 'kp-math-fractions',
      subject_code: 'MATH',
      subject_name: '数学',
      knowledge_point_code: 'MATH-PRI-FRACTIONS',
      name: '分数',
      description: '小学分数课程骨架',
      grade_band_code: 'PRIMARY',
      grade_band_name: '小学',
      domain_code: 'MATH_PRI_FRACTION_RATIO',
      domain_name: '小数、分数与比例',
      unit_code: 'PRIMARY_DECIMALS_FRACTIONS',
      unit_name: '小数、分数与百分数',
      default_difficulty: 'L2',
      curriculum_source_name: '互动式学习 V1 产品课程骨架',
      curriculum_source_ref: 'section-4.2/math/primary/分数',
    },
    {
      id: 'kp-physics-speed',
      subject_code: 'PHYSICS',
      subject_name: '物理',
      knowledge_point_code: 'PHYSICS-SPEED',
      name: '速度',
      description: '用路程与时间描述运动快慢',
      grade_band_code: 'JUNIOR_SECONDARY',
      grade_band_name: '初中',
      domain_code: 'PHY_JUN_MOTION',
      domain_name: '测量与机械运动',
      unit_code: 'JUNIOR_SECONDARY_MEASUREMENT_MOTION',
      unit_name: '测量与机械运动',
      default_difficulty: 'L2',
      curriculum_source_name: '互动式学习 V1 产品课程骨架',
      curriculum_source_ref: 'section-7/physics/junior/速度',
    },
  ],
  sources: [
    {
      id: 'source-original',
      name: 'V1 原创示范课程',
      source_type: 'INTERNAL_RULE',
      license_code: 'INTERNAL-ORIGINAL',
      attribution: 'AI Learning Tutor',
    },
  ],
}

it('generates Owner-only draft questions from released curriculum options', async () => {
  const generated = [
    { question_id: 'question-1', status: 'DRAFT', prompt: '三份资料和装订费共36元，怎样表示等量关系？' },
    { question_id: 'question-2', status: 'DRAFT', prompt: '四张门票和服务费共50元，怎样表示单价？' },
  ]
  const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const path = String(input)
    if (path === '/api/v1/owner/content/generation-options') {
      return { ok: true, status: 200, json: async () => generationOptions } as Response
    }
    if (path === '/api/v1/owner/content/generate') {
      return { ok: true, status: 201, json: async () => ({ records: generated }) } as Response
    }
    void init
    return { ok: true, status: 200, json: async () => ({ records: [] }) } as Response
  })
  vi.stubGlobal('fetch', fetchMock)

  const wrapper = mount(AdminContentPage)
  await flushPromises()
  await wrapper.get('input[name="generation-count"]').setValue('2')
  await wrapper.get('textarea[name="generation-requirements"]').setValue('使用校园生活场景')
  await wrapper.get('form[aria-label="AI 生成题目"]').trigger('submit')
  await flushPromises()

  const generationCall = fetchMock.mock.calls.find(([input]) => String(input) === '/api/v1/owner/content/generate')
  expect(generationCall).toBeTruthy()
  expect(JSON.parse(String(generationCall?.[1]?.body))).toEqual({
    knowledge_point_id: 'kp-math-equation',
    source_id: 'source-original',
    difficulty: 'L2',
    question_type: 'FREE_TEXT',
    count: 2,
    requirements: '使用校园生活场景',
  })
  expect(wrapper.text()).toContain('本次已生成 2 道 DRAFT')
  expect(wrapper.text()).toContain(generated[0].prompt)
  expect(wrapper.text()).not.toContain('correct_answer')
  expect(wrapper.text()).not.toContain('teacher_private')
})

it('disables generation when the server has no configured generator', async () => {
  vi.stubGlobal('fetch', vi.fn(async (input: string | URL | Request) => {
    if (String(input) === '/api/v1/owner/content/generation-options') {
      return { ok: true, status: 200, json: async () => ({ ...generationOptions, generator_available: false }) } as Response
    }
    return { ok: true, status: 200, json: async () => ({ records: [] }) } as Response
  }))

  const wrapper = mount(AdminContentPage)
  await flushPromises()

  expect(wrapper.get('form[aria-label="AI 生成题目"] button[type="submit"]').attributes('disabled')).toBeDefined()
  expect(wrapper.text()).toContain('内容生成模型未配置')
})

it('filters the expanded catalog by grade band, domain, and knowledge-point search', async () => {
  vi.stubGlobal('fetch', vi.fn(async (input: string | URL | Request) => {
    if (String(input) === '/api/v1/owner/content/generation-options') {
      return { ok: true, status: 200, json: async () => generationOptions } as Response
    }
    return { ok: true, status: 200, json: async () => ({ records: [] }) } as Response
  }))

  const wrapper = mount(AdminContentPage)
  await flushPromises()

  expect(wrapper.text()).toContain('3 个已发布知识点')
  expect(wrapper.get('select[name="generation-domain"]').text()).toContain('方程与不等式 · 1 个知识点')
  await wrapper.get('select[name="generation-grade-band"]').setValue('PRIMARY')
  await wrapper.get('input[name="generation-knowledge-search"]').setValue('MATH-PRI-FRACTIONS')

  const knowledgePoint = wrapper.get('select[name="generation-knowledge-point"]')
  expect((knowledgePoint.element as HTMLSelectElement).value).toBe('kp-math-fractions')
  expect(knowledgePoint.text()).toContain('小数、分数与百分数 · 分数 [MATH-PRI-FRACTIONS]')
  expect(wrapper.text()).toContain('来源：互动式学习 V1 产品课程骨架')
})
