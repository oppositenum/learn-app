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

it('shows the §1.1 breakdown and release history only for knowledge points that have one', async () => {
  const record = { id: 'question-1', status: 'RELEASED', content_version: 'v5', subject: 'MATH', knowledge_point: '一元一次方程', prompt: '题面', automatic_validation_passed: true, secondary_review_passed: true }
  vi.stubGlobal('fetch', vi.fn(async (input: string | URL | Request) => {
    if (String(input) === '/api/v1/owner/content/generation-options') {
      return { ok: true, status: 200, json: async () => generationOptions } as Response
    }
    return { ok: true, status: 200, json: async () => ({
      records: [record, { ...record, id: 'question-2', knowledge_point: '分数' }],
      knowledge_points: [{
        knowledge_point_code: 'MATH-LINEAR-EQUATION',
        knowledge_point: '一元一次方程',
        subject: 'MATH',
        foundation: '四则运算熟练、负数概念、等式性质',
        difficulty_points: '把文字问题翻译成方程；等式两边同时运算；检验答案',
        common_stuck_point: '不会算不是主要问题，而是看不到题目里的等量关系，所以不知道为什么要列方程',
        release_records: [{ from_status: 'AI_REVIEWED', to_status: 'RELEASED', at: '2026-09-20T08:00:00Z' }],
      }],
    }) } as Response
  }))

  const wrapper = mount(AdminContentPage)
  await flushPromises()

  const breakdowns = wrapper.findAll('[data-testid="knowledge-point-breakdown"]')
  expect(breakdowns).toHaveLength(1)
  const breakdown = breakdowns[0]
  expect(breakdown.text()).toContain('MATH-LINEAR-EQUATION')
  expect(breakdown.findAll('dt').map((term) => term.text())).toEqual(['基础', '难度点', '常见卡点'])
  expect(breakdown.text()).toContain('四则运算熟练、负数概念、等式性质')
  expect(breakdown.text()).toContain('把文字问题翻译成方程；等式两边同时运算；检验答案')
  expect(breakdown.text()).toContain('看不到题目里的等量关系')
  const release = breakdown.get('[data-testid="knowledge-point-release"]')
  expect(release.text()).toContain('AI_REVIEWED → RELEASED')
  expect(release.get('time').attributes('datetime')).toBe('2026-09-20T08:00:00Z')
  expect(wrapper.text()).not.toContain('findings')
})

it('shows the four why-it-matters items and the three scenes of a knowledge point', async () => {
  const whyItMatters = {
    daily_life: '用来把固定费用和按数量增加的费用分开，比如门票加服务费、租车起步价加每公里费用。',
    human_world: '用来比较两种计费哪个更合适，比如两家打印店、两种租车方案。',
    future_learning: '后面学习一次函数时，会用这里的固定起点和每增加 1 份的变化。',
    career_or_science: '工程、财务和实验记录里，常用这种固定量加相同变化量来估算。',
  }
  const scenes = [
    { connection_type: 'DAILY_LIFE', title: '门票加服务费、租车起步价加每公里费用', explanation: whyItMatters.daily_life },
    { connection_type: 'HUMAN_WORLD', title: '两家打印店、两种租车方案', explanation: whyItMatters.human_world },
    { connection_type: 'SCIENCE_OR_CAREER', title: '工程、财务和实验记录', explanation: whyItMatters.career_or_science },
  ]
  vi.stubGlobal('fetch', vi.fn(async (input: string | URL | Request) => {
    if (String(input) === '/api/v1/owner/content/generation-options') {
      return { ok: true, status: 200, json: async () => generationOptions } as Response
    }
    return { ok: true, status: 200, json: async () => ({
      records: [],
      knowledge_points: [{
        knowledge_point_code: 'MATH-LINEAR-EQUATION',
        knowledge_point: '一元一次方程',
        subject: 'MATH',
        foundation: '四则运算熟练、负数概念、等式性质',
        difficulty_points: '把文字问题翻译成方程；等式两边同时运算；检验答案',
        common_stuck_point: '不会算不是主要问题，而是看不到题目里的等量关系，所以不知道为什么要列方程',
        why_it_matters: whyItMatters,
        world_connections: scenes,
        release_records: [],
      }],
    }) } as Response
  }))

  const wrapper = mount(AdminContentPage)
  await flushPromises()

  const items = wrapper.get('[data-testid="knowledge-point-why-it-matters"]').findAll('li')
  expect(items.map((item) => item.text())).toEqual([
    `日常生活 ${whyItMatters.daily_life}`,
    `人与世界 ${whyItMatters.human_world}`,
    `后续学习 ${whyItMatters.future_learning}`,
    `职业与科学 ${whyItMatters.career_or_science}`,
  ])
  const rendered = wrapper.findAll('[data-testid="knowledge-point-world-connection"]')
  expect(rendered.map((scene) => scene.attributes('data-connection-type'))).toEqual(['DAILY_LIFE', 'HUMAN_WORLD', 'SCIENCE_OR_CAREER'])
  rendered.forEach((scene, index) => {
    expect(scene.text()).toContain(scenes[index].title)
    expect(scene.text()).toContain(scenes[index].explanation)
  })
})

it('shows the five answers of each solution method only where a knowledge point has methods', async () => {
  const methods = [
    {
      sequence: 1,
      method_name: '假设法',
      first_look: '先看一共有多少个头、一共有多少只脚。',
      why_this_method: '先当成全是鸡，脚的差额就知道兔子有几只，不用一开始列两个未知数。',
      method_path: '全假设，算差额，用每只多出来的脚数去除，再得到另一种。',
      check_where: '用两种数量分别乘脚数，加起来是否等于总脚数。',
      more_direct: '熟练以后可以直接列方程。',
    },
    {
      sequence: 2,
      method_name: '列方程法',
      first_look: '先看哪个量不知道，哪个量和它按固定关系一起变。',
      why_this_method: '关系已经是一次的，设未知数比反复试数更清楚。',
      method_path: '设未知数，写固定量加每份变化量，让它等于总量，再解。',
      check_where: '把求出的数代回原式，看等号两边是否相同。',
      more_direct: '数字很小的时候也可以画图或列表。',
    },
  ]
  const point = {
    knowledge_point_code: 'MATH-LINEAR-EQUATION',
    knowledge_point: '一元一次方程',
    subject: 'MATH',
    foundation: '四则运算熟练、负数概念、等式性质',
    difficulty_points: '',
    common_stuck_point: '',
    release_records: [],
  }
  vi.stubGlobal('fetch', vi.fn(async (input: string | URL | Request) => {
    if (String(input) === '/api/v1/owner/content/generation-options') {
      return { ok: true, status: 200, json: async () => generationOptions } as Response
    }
    return { ok: true, status: 200, json: async () => ({
      records: [],
      knowledge_points: [
        { ...point, solution_methods: methods },
        { ...point, knowledge_point_code: 'MATH-FRACTION-COMMON-DENOMINATOR', knowledge_point: '分数通分', solution_methods: [] },
      ],
    }) } as Response
  }))

  const wrapper = mount(AdminContentPage)
  await flushPromises()

  const [withMethods, withoutMethods] = wrapper.findAll('[data-testid="knowledge-point-breakdown"]')
  const rendered = withMethods.findAll('[data-testid="knowledge-point-solution-method"]')
  expect(rendered).toHaveLength(2)
  rendered.forEach((method, index) => {
    expect(method.get('p').text()).toBe(methods[index].method_name)
    expect(method.findAll('ul > li').map((line) => line.text())).toEqual([
      `第一眼看什么 ${methods[index].first_look}`,
      `为什么用这个方法 ${methods[index].why_this_method}`,
      `路径是什么 ${methods[index].method_path}`,
      `在哪里检查 ${methods[index].check_where}`,
      `有没有更直接的做法 ${methods[index].more_direct}`,
    ])
  })
  expect(withoutMethods.find('[data-testid="knowledge-point-solution-method"]').exists()).toBe(false)
  expect(withoutMethods.findAll('h3').some((heading) => heading.text() === '解法思路')).toBe(false)
})

it('shows no breakdown headings when no knowledge point has one', async () => {
  vi.stubGlobal('fetch', vi.fn(async (input: string | URL | Request) => {
    if (String(input) === '/api/v1/owner/content/generation-options') {
      return { ok: true, status: 200, json: async () => generationOptions } as Response
    }
    return { ok: true, status: 200, json: async () => ({ records: [], knowledge_points: [] }) } as Response
  }))

  const wrapper = mount(AdminContentPage)
  await flushPromises()

  expect(wrapper.find('[data-testid="knowledge-point-breakdown"]').exists()).toBe(false)
  for (const heading of ['基础', '难度点', '常见卡点', '为什么重要', '生活场景', '解法思路', '发布记录']) {
    expect(wrapper.findAll('dt, h3').some((node) => node.text() === heading)).toBe(false)
  }
})
