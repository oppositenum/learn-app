import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter, RouterView } from 'vue-router'
import { afterEach, expect, test, vi } from 'vitest'

import type { StudentSession } from '../../api/student'
import { useLearningStore } from '../../stores/learning'
import StudentSessionPage from './StudentSessionPage.vue'
import StudentSupplyPage from './StudentSupplyPage.vue'

function session(overrides: Partial<StudentSession> = {}): StudentSession {
  return {
    id: 'session-1',
    version: 1,
    timing_version: 1,
    subject_code: 'MATH',
    subject_name: '数学',
    knowledge_point: '分数通分',
    difficulty: 'L1',
    question_id: 'question-1',
    prompt: '三分之一和四分之一的小格一样大吗？',
    scene: {},
    input_schema: {},
    started_at: '2026-08-26T12:00:00Z',
    target_minutes: 20,
    status: 'PAUSED',
    active_seconds: 20,
    current_active_seconds: 0,
    timing_observed_at: '2026-08-26T12:00:20Z',
    state: 'EXPLAIN',
    socratic_round: 2,
    timeline: [{ sequence: 1, actor: 'TUTOR', action: 'EXPLAIN', message: '先观察句子里的动作。', at: '2026-08-26T12:00:10Z' }],
    ...overrides,
  }
}

function response(body: unknown, status = 200): Response {
  return { ok: status >= 200 && status < 300, status, json: async () => body } as Response
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((next) => { resolve = next })
  return { promise, resolve }
}

async function mountPage(fetch: typeof globalThis.fetch) {
  setActivePinia(createPinia())
  vi.stubGlobal('fetch', fetch)
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/student', component: { template: '<div>首页</div>' } },
      { path: '/student/session/:id', component: StudentSessionPage },
      { path: '/student/session/:id/supply', component: { template: '<div>补给站</div>' } },
      { path: '/student/session/:id/voice', component: { template: '<div>语音讲解</div>' } },
    ],
  })
  await router.push('/student/session/session-1')
  await router.isReady()
  const wrapper = mount(StudentSessionPage, { global: { plugins: [router] } })
  await flushPromises()
  return { learning: useLearningStore(), router, wrapper }
}

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
	Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1024 })
	Object.defineProperty(window, 'innerHeight', { configurable: true, value: 768 })
})

test('keeps paused answer controls in the DOM and waits for an explicit resume', async () => {
  const requests: string[] = []
  const { wrapper } = await mountPage(vi.fn(async (input: string | URL | Request) => {
    requests.push(String(input))
    return response(session())
  }))

  expect(requests).toEqual(['/api/v1/student/sessions/session-1'])
  expect(requests.some((path) => path.endsWith('/resume'))).toBe(false)
  expect(wrapper.get('[data-testid="classroom-composer"]').attributes('data-layout-contract')).toBe('normal-flow')
  expect(wrapper.get('[data-testid="answer-controls"]').attributes()).toMatchObject({ disabled: '', 'aria-disabled': 'true' })
  expect(wrapper.find('#student-answer').exists()).toBe(true)
  expect(wrapper.get('[data-testid="resume-session"]').attributes('disabled')).toBeUndefined()
  expect(wrapper.get('[data-testid="resume-session"]').text()).toContain('继续探索')
  wrapper.unmount()
})

test('renders stage progress and retries a structured draft with the same operation id', async () => {
  let answerRequests = 0
  let advanced = false
  const submitted: Array<Record<string, unknown>> = []
  const stageInteraction = {
    version: 'student-interaction-v1' as const,
    renderer: 'SINGLE_CHOICE' as const,
    scene: {
      version: 'student-interaction-v1' as const,
      renderer: 'SINGLE_CHOICE' as const,
      accessible_fallback: '从甲和乙中选择一项。',
      options: [{ id: 'a', label: '甲' }, { id: 'b', label: '乙' }],
    },
    answer_schema: {}, accessible_fallback: '从甲和乙中选择一项。', fallback: false,
  }
  const { learning, wrapper } = await mountPage(vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const path = String(input)
    if (path.endsWith('/answers')) {
      answerRequests++
      submitted.push(JSON.parse(String(init?.body)) as Record<string, unknown>)
      if (answerRequests === 1) return response({ code: 'TUTOR_REVIEW_TEMPORARILY_UNAVAILABLE' }, 503)
      advanced = true
      return response({
        session_id: 'session-1', version: 2, timing_version: 1, action: 'VARIANT', stage: 'VARIANT',
        task_id: 'question-2', task_version: 'content-v1', evidence_kind: 'INDEPENDENT', stage_completed: true,
        message: '这一阶段已独立完成，继续下一阶段。', status: 'ACTIVE', active_seconds: 20,
        current_active_seconds: 1, timing_observed_at: '2026-08-26T12:00:21Z',
      })
    }
    return response(session({
      version: advanced ? 2 : 1,
      status: 'ACTIVE', state: advanced ? 'VARIANT' : 'ORIGINAL',
      question_id: advanced ? 'question-2' : 'question-1',
      prompt: advanced ? '第二阶段任务' : '第一阶段任务',
      interaction: stageInteraction,
      stage_flow: { version: 'classroom-stage-flow-v1', stage: advanced ? 'VARIANT' : 'ORIGINAL', task_version: 'content-v1' },
    }))
  }))

  expect(wrapper.get('[aria-current="step"]').text()).toBe('原题')
  expect(wrapper.get('details').text()).toContain('从甲和乙中选择一项。')
  await wrapper.findAll<HTMLInputElement>('input[type="radio"]')[0].setValue(true)
  await wrapper.get('form').trigger('submit')
  await flushPromises()

  expect(learning.error).toBe('老师正在想，等一下再试一次')
  expect(wrapper.findAll<HTMLInputElement>('input[type="radio"]')[0].element.checked).toBe(true)

  await wrapper.get('form').trigger('submit')
  await flushPromises()

  expect(answerRequests).toBe(2)
  expect(submitted[0].operation_id).toBe(submitted[1].operation_id)
  expect(submitted[0]).toMatchObject({
    stage: 'ORIGINAL', task_id: 'question-1', task_version: 'content-v1',
    response: { selected_option_ids: ['a'] },
  })
  expect(submitted[0]).not.toHaveProperty('answer')
  expect(wrapper.get('[aria-current="step"]').text()).toBe('变式')
  expect(learning.structuredDraft).toEqual({ selected_option_ids: [] })
  wrapper.unmount()
})

test('uses the text composer for a server-declared fallback material', async () => {
  const { wrapper } = await mountPage(vi.fn(async () => response(session({
    status: 'ACTIVE', state: 'ASK',
    interaction: {
      version: 'student-text-fallback-v1', renderer: 'TEXT_FALLBACK', answer_schema: { type: 'string' },
      accessible_fallback: '保留的文字任务', fallback: true,
    },
  }))))

  expect(wrapper.find('[data-renderer]').exists()).toBe(false)
  expect(wrapper.find('#student-answer').exists()).toBe(true)
  wrapper.unmount()
})

test('keeps an invalidated stage task answer-free and recoverable', async () => {
  const { wrapper } = await mountPage(vi.fn(async () => response(session({
    status: 'ACTIVE', state: 'ORIGINAL',
    interaction: {
      version: 'student-text-fallback-v1', renderer: 'TEXT_FALLBACK', answer_schema: { type: 'string' },
      accessible_fallback: '保留的文字任务', fallback: true,
    },
    stage_flow: { version: 'classroom-stage-flow-v1', stage: 'ORIGINAL', task_version: 'content-v1' },
  }))))

  expect(wrapper.find('form').exists()).toBe(false)
  expect(wrapper.get('[role="alert"]').text()).toContain('本次不会记录答题证据')
  expect(wrapper.get('[role="alert"] button').text()).toContain('重新加载任务')
  wrapper.unmount()
})

test('renders the server safety fallback and transparent parent notice', async () => {
	const privateInput = 'PRIVATE_SAFETY_INPUT_CANARY'
	const { learning, wrapper } = await mountPage(vi.fn((input: string | URL | Request) => {
		const path = String(input)
		if (path.endsWith('/answers')) {
			return Promise.resolve(response({
				session_id: 'session-1', version: 1, timing_version: 1,
				action: 'ASK', socratic_round: 0, message: '请先去找身边可信任的大人。',
				status: 'ACTIVE', active_seconds: 20, current_active_seconds: 0,
				timing_observed_at: '2026-08-26T12:00:20Z',
				safety: {
					policy_version: 'minor-safety-v1', category: 'SELF_HARM', severity: 'CRITICAL',
					fixed_action: 'SEEK_URGENT_HELP', parent_notified: true,
				},
			}))
		}
		return Promise.resolve(response(session({ status: 'ACTIVE', state: 'ASK' })))
	}))
	await wrapper.get('#student-answer').setValue(privateInput)

	await wrapper.get('form').trigger('submit')
	await flushPromises()

	const notice = wrapper.get('[data-testid="safety-notice"]')
	expect(notice.text()).toContain('请先去找身边可信任的大人。')
	expect(notice.text()).toContain('已按安全规则通知家长')
	expect(notice.text()).not.toContain(privateInput)
	expect(learning.timeline.some((turn) => turn.text.includes(privateInput))).toBe(false)
	expect(wrapper.get<HTMLInputElement>('#student-answer').element.value).toBe('')
	wrapper.unmount()
})

test('deduplicates an explicit resume and enables answers only after it succeeds', async () => {
  const resume = deferred<Response>()
  const requests: string[] = []
  const { learning, wrapper } = await mountPage(vi.fn((input: string | URL | Request) => {
    const path = String(input)
    requests.push(path)
    if (path.endsWith('/resume')) return resume.promise
    return Promise.resolve(response(session()))
  }))

  const button = wrapper.get('[data-testid="resume-session"]')
  const firstClick = button.trigger('click')
  const secondClick = button.trigger('click')
  await Promise.resolve()

  expect(requests.filter((path) => path.endsWith('/resume'))).toHaveLength(1)
  expect(learning.status).toBe('PAUSED')
  expect(wrapper.get('[data-testid="answer-controls"]').attributes('disabled')).toBe('')
	await wrapper.get('form').trigger('submit')
	expect(requests.some((path) => path.endsWith('/answers'))).toBe(false)

  resume.resolve(response({
    session_id: 'session-1',
    version: 1,
    timing_version: 2,
    status: 'ACTIVE',
    active_seconds: 20,
    current_active_seconds: 0,
    active_since: '2026-08-26T12:00:21Z',
    timing_observed_at: '2026-08-26T12:00:21Z',
  }))
  await Promise.all([firstClick, secondClick])
  await flushPromises()

  expect(learning.status).toBe('ACTIVE')
  expect(wrapper.get('[data-testid="answer-controls"]').attributes('disabled')).toBeUndefined()
  expect(wrapper.find('[data-testid="resume-session"]').exists()).toBe(false)
	expect(wrapper.get('#student-answer').attributes('disabled')).toBeUndefined()
	expect(wrapper.get('[aria-label="使用语音回答"]').attributes('disabled')).toBeUndefined()
	for (const label of ['一点提示', '我不会']) {
		const action = wrapper.findAll('button').find((candidate) => candidate.text().includes(label))
		expect(action?.attributes('disabled')).toBeUndefined()
	}
  wrapper.unmount()
})

test('keeps a readable recovery action after failure and allows retry', async () => {
  let resumeRequests = 0
  const { learning, wrapper } = await mountPage(vi.fn(async (input: string | URL | Request) => {
    const path = String(input)
    if (!path.endsWith('/resume')) return response(session())
    resumeRequests++
    if (resumeRequests === 1) return response(null, 500)
    return response({
      session_id: 'session-1',
      version: 1,
      timing_version: 2,
      status: 'ACTIVE',
      active_seconds: 20,
      current_active_seconds: 0,
      active_since: '2026-08-26T12:00:21Z',
      timing_observed_at: '2026-08-26T12:00:21Z',
    })
  }))

  await wrapper.get('[data-testid="resume-session"]').trigger('click')
  await flushPromises()

  expect(learning.status).toBe('PAUSED')
  expect(wrapper.get('[role="alert"]').text()).toContain('学习数据暂时不可用（500）')
  expect(wrapper.get('[data-testid="resume-session"]').attributes('disabled')).toBeUndefined()

  await wrapper.get('[data-testid="resume-session"]').trigger('click')
  await flushPromises()

  expect(resumeRequests).toBe(2)
  expect(learning.status).toBe('ACTIVE')
  expect(wrapper.find('[role="alert"]').exists()).toBe(false)
  wrapper.unmount()
})

test('keeps the answer and retry controls after a temporary Tutor review failure', async () => {
  let supportRequests = 0
  const { learning, wrapper } = await mountPage(vi.fn(async (input: string | URL | Request) => {
    const path = String(input)
    if (path.endsWith('/support')) {
      supportRequests++
      if (supportRequests === 1) return response({ code: 'TUTOR_REVIEW_TEMPORARILY_UNAVAILABLE' }, 503)
      return response({
        session_id: 'session-1',
        version: 2,
        timing_version: 2,
        action: 'HINT',
        socratic_round: 2,
        message: '先找一找两种分数共同的小格。',
        status: 'ACTIVE',
        active_seconds: 20,
        current_active_seconds: 3,
        timing_observed_at: '2026-08-26T12:00:23Z',
      })
    }
    return response(session({
      version: supportRequests >= 2 ? 2 : 1,
      timing_version: supportRequests >= 2 ? 2 : 1,
      status: 'ACTIVE',
      state: supportRequests >= 2 ? 'HINT' : 'ASK',
      current_active_seconds: supportRequests >= 2 ? 3 : 0,
    }))
  }))
  const textarea = wrapper.get<HTMLTextAreaElement>('#student-answer')
  await textarea.setValue('我先把小格分成一样大')
  const hint = () => wrapper.findAll('button').find((button) => button.text().includes('一点提示'))!

  await hint().trigger('click')
  await flushPromises()

  expect(learning.error).toBe('老师正在想，等一下再试一次')
  expect(wrapper.get('[role="alert"]').text()).toBe('老师正在想，等一下再试一次')
  expect(textarea.element.value).toBe('我先把小格分成一样大')
  expect(wrapper.get('[data-testid="answer-controls"]').attributes('disabled')).toBeUndefined()
  expect(hint().attributes('disabled')).toBeUndefined()
  expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeUndefined()

  await hint().trigger('click')
  await flushPromises()

  expect(supportRequests).toBe(2)
  expect(learning.error).toBe('')
  expect(wrapper.find('[role="alert"]').exists()).toBe(false)
  expect(textarea.element.value).toBe('我先把小格分成一样大')
  expect(hint().attributes('disabled')).toBeUndefined()
  wrapper.unmount()
})

test('keeps a submitted answer available when Tutor review is temporarily unavailable', async () => {
  let answerRequests = 0
  const { learning, wrapper } = await mountPage(vi.fn(async (input: string | URL | Request) => {
    const path = String(input)
    if (path.endsWith('/answers')) {
      answerRequests++
      if (answerRequests === 1) return response({ code: 'TUTOR_REVIEW_TEMPORARILY_UNAVAILABLE' }, 503)
      return response({
        session_id: 'session-1',
        version: 2,
        timing_version: 2,
        action: 'PROBE',
        socratic_round: 3,
        message: '你准备怎样让两种小格一样大？',
        status: 'ACTIVE',
        active_seconds: 20,
        current_active_seconds: 4,
        timing_observed_at: '2026-08-26T12:00:24Z',
      })
    }
    return response(session({
      version: answerRequests >= 2 ? 2 : 1,
      timing_version: answerRequests >= 2 ? 2 : 1,
      status: 'ACTIVE',
      state: answerRequests >= 2 ? 'PROBE' : 'ASK',
      current_active_seconds: answerRequests >= 2 ? 4 : 0,
    }))
  }))
  const textarea = wrapper.get<HTMLTextAreaElement>('#student-answer')
  await textarea.setValue('我觉得要先通分')

  await wrapper.get('form').trigger('submit')
  await flushPromises()

  expect(learning.error).toBe('老师正在想，等一下再试一次')
  expect(textarea.element.value).toBe('我觉得要先通分')
  expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeUndefined()

  await wrapper.get('form').trigger('submit')
  await flushPromises()

  expect(answerRequests).toBe(2)
  expect(learning.error).toBe('')
  expect(textarea.element.value).toBe('')
  wrapper.unmount()
})

test('keeps controls and typed text available when a HINT must be rephrased', async () => {
  let supportRequests = 0
  const { learning, wrapper } = await mountPage(vi.fn(async (input: string | URL | Request) => {
    const path = String(input)
    if (path.endsWith('/support')) {
      supportRequests++
      return response({ code: 'TUTOR_OUTPUT_REPHRASE_REQUIRED' }, 422)
    }
    return response(session({ status: 'ACTIVE', state: 'ASK', current_active_seconds: 0 }))
  }))
  const textarea = wrapper.get<HTMLTextAreaElement>('#student-answer')
  await textarea.setValue('我正在整理自己的思路')
  const hint = wrapper.findAll('button').find((button) => button.text().includes('一点提示'))!

  await hint.trigger('click')
  await flushPromises()

  expect(supportRequests).toBe(1)
  expect(learning.error).toBe('刚才的提示不太合适，老师换个问法。请再点一次『一点提示』')
  expect(wrapper.get('[role="alert"]').text()).toBe('刚才的提示不太合适，老师换个问法。请再点一次『一点提示』')
  expect(wrapper.text()).not.toContain('学习数据暂时不可用（422）')
  expect(textarea.element.value).toBe('我正在整理自己的思路')
  expect(wrapper.get('[data-testid="answer-controls"]').attributes('disabled')).toBeUndefined()
  expect(hint.attributes('disabled')).toBeUndefined()
  expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeUndefined()
  wrapper.unmount()
})

test('keeps controls and typed text available when an EXPLAIN must be rephrased', async () => {
  let supportRequests = 0
  const { learning, wrapper } = await mountPage(vi.fn(async (input: string | URL | Request) => {
    const path = String(input)
    if (path.endsWith('/support')) {
      supportRequests++
      return response({ code: 'TUTOR_OUTPUT_REPHRASE_REQUIRED' }, 422)
    }
    return response(session({ status: 'ACTIVE', state: 'ASK', current_active_seconds: 0 }))
  }))
  const textarea = wrapper.get<HTMLTextAreaElement>('#student-answer')
  await textarea.setValue('我还在整理自己的想法')
  const explain = wrapper.findAll('button').find((button) => button.text().includes('我不会'))!

  await explain.trigger('click')
  await flushPromises()

  expect(supportRequests).toBe(1)
  expect(learning.error).toBe('刚才的讲解不太合适，老师换个说法。请再点一次『我不会』')
  expect(wrapper.get('[role="alert"]').text()).toBe('刚才的讲解不太合适，老师换个说法。请再点一次『我不会』')
  expect(wrapper.text()).not.toContain('学习数据暂时不可用（422）')
  expect(wrapper.text()).not.toContain('请再提交一次')
  expect(textarea.element.value).toBe('我还在整理自己的想法')
  expect(wrapper.get('[data-testid="answer-controls"]').attributes('disabled')).toBeUndefined()
  expect(explain.attributes('disabled')).toBeUndefined()
  expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeUndefined()
  wrapper.unmount()
})

test('keeps a submitted answer and controls available when the response must be rephrased', async () => {
  let answerRequests = 0
  const { learning, wrapper } = await mountPage(vi.fn(async (input: string | URL | Request) => {
    const path = String(input)
    if (path.endsWith('/answers')) {
      answerRequests++
      return response({ code: 'TUTOR_OUTPUT_REPHRASE_REQUIRED' }, 422)
    }
    return response(session({ status: 'ACTIVE', state: 'ASK', current_active_seconds: 0 }))
  }))
  const textarea = wrapper.get<HTMLTextAreaElement>('#student-answer')
  await textarea.setValue('这是我还要继续检查的想法')

  await wrapper.get('form').trigger('submit')
  await flushPromises()

  expect(answerRequests).toBe(1)
  expect(learning.error).toBe('刚才的回应不太合适，老师换个问法。请再提交一次')
  expect(wrapper.get('[role="alert"]').text()).toBe('刚才的回应不太合适，老师换个问法。请再提交一次')
  expect(wrapper.text()).not.toContain('课堂暂时无法提交（422）')
  expect(textarea.element.value).toBe('这是我还要继续检查的想法')
  expect(wrapper.get('[data-testid="answer-controls"]').attributes('disabled')).toBeUndefined()
  expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeUndefined()
  wrapper.unmount()
})

test.each([
  { type: 'HINT' as const, label: '一点提示' },
  { type: 'EXPLAIN' as const, label: '我不会' },
])('keeps controls and typed text after $type generation throttling', async ({ label }) => {
  let supportRequests = 0
  const { learning, router, wrapper } = await mountPage(vi.fn(async (input: string | URL | Request) => {
    const path = String(input)
    if (path.endsWith('/support')) {
      supportRequests++
      return response({ code: 'TUTOR_GENERATION_BUSY' }, 503)
    }
    return response(session({ status: 'ACTIVE', state: 'ASK', current_active_seconds: 0 }))
  }))
  const textarea = wrapper.get<HTMLTextAreaElement>('#student-answer')
  await textarea.setValue('尚未提交的课堂草稿')
  const action = wrapper.findAll('button').find((button) => button.text().includes(label))!

  await action.trigger('click')
  await flushPromises()

  expect(supportRequests).toBe(1)
  expect(learning.error).toBe('现在有点挤，老师马上就来。请稍等一下再试一次')
  expect(wrapper.get('[role="alert"]').text()).toBe('现在有点挤，老师马上就来。请稍等一下再试一次')
  expect(wrapper.text()).not.toContain('学习数据暂时不可用（503）')
  expect(textarea.element.value).toBe('尚未提交的课堂草稿')
  expect(wrapper.get('[data-testid="answer-controls"]').attributes('disabled')).toBeUndefined()
  expect(action.attributes('disabled')).toBeUndefined()
  expect(router.currentRoute.value.path).toBe('/student/session/session-1')
  wrapper.unmount()
})

test('keeps answer controls and typed text after answer-generation throttling', async () => {
  let answerRequests = 0
  const { learning, wrapper } = await mountPage(vi.fn(async (input: string | URL | Request) => {
    const path = String(input)
    if (path.endsWith('/answers')) {
      answerRequests++
      return response({ code: 'TUTOR_GENERATION_BUSY' }, 503)
    }
    return response(session({ status: 'ACTIVE', state: 'ASK', current_active_seconds: 0 }))
  }))
  const textarea = wrapper.get<HTMLTextAreaElement>('#student-answer')
  await textarea.setValue('尚未提交的课堂草稿')

  await wrapper.get('form').trigger('submit')
  await flushPromises()

  expect(answerRequests).toBe(1)
  expect(learning.error).toBe('现在有点挤，老师马上就来。请稍等一下再试一次')
  expect(wrapper.get('[role="alert"]').text()).toBe('现在有点挤，老师马上就来。请稍等一下再试一次')
  expect(textarea.element.value).toBe('尚未提交的课堂草稿')
  expect(wrapper.get('[data-testid="answer-controls"]').attributes('disabled')).toBeUndefined()
  expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeUndefined()
  wrapper.unmount()
})

test('preserves an answer draft across an EXPLAIN supply round trip', async () => {
  let supportRequests = 0
  const fetch = vi.fn(async (input: string | URL | Request) => {
    const path = String(input)
    if (path.endsWith('/support')) {
      supportRequests++
      return response({
        session_id: 'session-1',
        version: 2,
        timing_version: 2,
        action: 'EXPLAIN',
        socratic_round: 2,
        message: '用一个相似场景观察同样的关系。',
        status: 'ACTIVE',
        active_seconds: 20,
        current_active_seconds: 3,
        timing_observed_at: '2026-08-26T12:00:23Z',
      })
    }
    return response(session({
      version: supportRequests ? 2 : 1,
      timing_version: supportRequests ? 2 : 1,
      status: 'ACTIVE',
      state: supportRequests ? 'EXPLAIN' : 'ASK',
      current_active_seconds: supportRequests ? 3 : 0,
      timeline: supportRequests ? [
        { sequence: 1, actor: 'TUTOR', action: 'ASK', message: '先说说你的想法。', at: '2026-08-26T12:00:00Z' },
        { sequence: 2, actor: 'TUTOR', action: 'EXPLAIN', message: '用一个相似场景观察同样的关系。', at: '2026-08-26T12:00:23Z' },
      ] : [
        { sequence: 1, actor: 'TUTOR', action: 'ASK', message: '先说说你的想法。', at: '2026-08-26T12:00:00Z' },
      ],
    }))
  })
  vi.stubGlobal('fetch', fetch)
  const pinia = createPinia()
  setActivePinia(pinia)
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/student/session/:id/supply', component: StudentSupplyPage },
      { path: '/student/session/:id', component: StudentSessionPage },
    ],
  })
  await router.push('/student/session/session-1')
  await router.isReady()
  const wrapper = mount(RouterView, { global: { plugins: [pinia, router] } })
  await flushPromises()

  await wrapper.get<HTMLTextAreaElement>('#student-answer').setValue('准备带回原题的草稿')
  const explain = wrapper.findAll('button').find((button) => button.text().includes('我不会'))!
  await explain.trigger('click')
  await flushPromises()

  expect(supportRequests).toBe(1)
  expect(router.currentRoute.value.path).toBe('/student/session/session-1/supply')
  expect(wrapper.findComponent(StudentSupplyPage).exists()).toBe(true)
  const returnLink = wrapper.findAll('a').find((link) => link.text().includes('返回原题'))!
  await returnLink.trigger('click')
  await flushPromises()

  expect(router.currentRoute.value.path).toBe('/student/session/session-1')
  expect(wrapper.get<HTMLTextAreaElement>('#student-answer').element.value).toBe('准备带回原题的草稿')
  wrapper.unmount()
})

test('uses unambiguous timers and renders the Tutor action title once', async () => {
  const { wrapper } = await mountPage(vi.fn(async () => response(session())))

  expect(wrapper.text()).toContain('本段用时')
  expect(wrapper.text()).toContain('本节累计')
  expect(wrapper.get('[data-testid="segment-timer"]').text()).toMatch(/^本段用时\s/)
  expect(wrapper.get('[data-testid="session-timer"]').text()).toMatch(/^本节累计\s/)
  expect(wrapper.text().match(/用相似例子讲一遍/g)).toHaveLength(1)
  wrapper.unmount()
})

test('keeps a variable-height composer after the Tutor turn in normal document flow', async () => {
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: 320 })
  Object.defineProperty(window, 'innerHeight', { configurable: true, value: 720 })
  const { wrapper } = await mountPage(vi.fn(async () => response(session())))
  const tutor = wrapper.get('[data-tutor-turn]')
  const composer = wrapper.get('[data-testid="classroom-composer"]')

  expect(tutor.element.compareDocumentPosition(composer.element) & Node.DOCUMENT_POSITION_FOLLOWING).not.toBe(0)
  expect(composer.attributes('data-layout-contract')).toBe('normal-flow')
  expect(composer.classes()).not.toContain('sticky')
  expect(composer.classes()).not.toContain('fixed')
  expect(composer.classes()).not.toContain('absolute')
  expect(composer.text()).toContain('这次探索已暂停')
  expect(composer.get('[data-testid="answer-controls"]').attributes('disabled')).toBe('')
  wrapper.unmount()
})
