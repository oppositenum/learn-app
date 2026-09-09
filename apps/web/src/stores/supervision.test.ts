import { setActivePinia, createPinia } from 'pinia'

import { formatTutorReason, useSupervisionStore } from './supervision'

class FakeWebSocket {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSED = 3
  static instances: FakeWebSocket[] = []

  readonly url: string
  readyState = FakeWebSocket.CONNECTING
  private listeners = new Map<string, Array<(event: { data?: string }) => void>>()

  constructor(url: string) {
    this.url = url
    FakeWebSocket.instances.push(this)
  }

  addEventListener(type: string, listener: (event: { data?: string }) => void) {
    const listeners = this.listeners.get(type) ?? []
    listeners.push(listener)
    this.listeners.set(type, listeners)
  }

  open() {
    this.readyState = FakeWebSocket.OPEN
    this.emit('open', {})
  }

  message(value: unknown) {
    this.emit('message', { data: JSON.stringify(value) })
  }

  close() {
    if (this.readyState === FakeWebSocket.CLOSED) return
    this.readyState = FakeWebSocket.CLOSED
    this.emit('close', {})
  }

  private emit(type: string, event: { data?: string }) {
    for (const listener of this.listeners.get(type) ?? []) listener(event)
  }
}

function liveSession(sessionID: string, answer = '第一次回答') {
  return {
    session_id: sessionID,
    student_id: 'student-new',
    subject: '化学',
    knowledge_point: '物理变化与化学变化',
    started_at: '2026-08-27T03:39:11Z',
    status: 'ACTIVE',
    current_state: 'SCAFFOLD',
    socratic_round: 2,
    engagement: 'NORMAL',
    question_prompt: '题目',
    correct_answer: { value: '铁钉生锈' },
    full_solution: '解析',
    detail_mode: 'LIVE',
    student_answer_visibility: 'SHORT_CURRENT',
    student_answer_preview: answer,
    answer_correct: false,
    error_type: 'STATE_CHANGE_IS_CHEMICAL',
    misconceptions: ['STATE_CHANGE_IS_CHEMICAL'],
    tutor_action: 'SCAFFOLD',
    tutor_reason: 'split the task into a smaller step',
    target_minutes: 10,
    mastery_state: 'LEARNING',
    mastery_score: 42,
    timeline: [],
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((next) => { resolve = next })
  return { promise, resolve }
}

it('renders canonical Tutor reasons as concise Parent-facing Chinese', () => {
  expect(formatTutorReason('student requested a parallel example without the original answer')).toBe('孩子表示不会，切换到不泄露原题答案的平行讲解。')
  expect(formatTutorReason('Socratic failed-round limit reached; explain with a parallel example')).toContain('三轮有效启发已到上限')
  expect(formatTutorReason('future canonical reason')).toBe('future canonical reason')
})

describe('Parent realtime supervision', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    FakeWebSocket.instances = []
    vi.stubGlobal('WebSocket', FakeWebSocket)
    vi.useFakeTimers()
  })

  afterEach(() => {
    useSupervisionStore().disconnect()
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('uses the REST preview and ignores answer bodies injected into realtime payloads', async () => {
    const fetchMock = vi.fn(async (input: string | URL | Request) => {
      const path = String(input)
      if (path.includes('/parent/children')) return {
        ok: true,
        json: async () => ({ children: [
          { student_id: 'student-old', display_name: '旧账号', grade_level: 7, active_session_id: 'session-old', subject: '数学', knowledge_point: '方程', started_at: '2026-08-27T01:00:00Z' },
          { student_id: 'student-new', display_name: '媛媛', grade_level: 7, active_session_id: 'session-new', subject: '化学', knowledge_point: '变化', started_at: '2026-08-27T03:39:11Z' },
        ] }),
      } as Response
      return { ok: true, json: async () => liveSession('session-new', 'REST 回答') } as Response
    })
    vi.stubGlobal('fetch', fetchMock)
    const store = useSupervisionStore()

    await store.initialize()
    expect(store.studentID).toBe('student-new')
    expect(FakeWebSocket.instances[0]?.url).toContain('/ws/parent/student-new')
    FakeWebSocket.instances[0]?.open()
    expect(store.connected).toBe(true)

    FakeWebSocket.instances[0]?.message({
      event_id: 'event-answer', student_id: 'student-new', session_id: 'session-new', type: 'TUTOR_ACTION_SELECTED',
      payload: { student_answer: 'WebSocket 实时想法', correct_answer: '铁钉生锈', error_type: 'STATE_CHANGE_IS_CHEMICAL', action: 'PROBE', round: 3 },
    })
    expect(store.studentAnswerPreview).toBe('REST 回答')
    expect(store.correctAnswer).toBe('铁钉生锈')
    expect(store.socraticRound).toBe(3)
    expect(store.timeline.at(-1)?.text).toBe('AI 已选择下一步教学动作')
    expect(store.timeline.some((item) => item.text === 'WebSocket 实时想法')).toBe(false)

    FakeWebSocket.instances[0]?.message({
      event_id: 'event-submitted', student_id: 'student-new', session_id: 'session-new', type: 'ANSWER_SUBMITTED',
      payload: { student_answer: 'WebSocket 实时想法' },
    })
    expect(store.timeline.some((item) => item.text === 'WebSocket 实时想法')).toBe(false)
    await vi.advanceTimersByTimeAsync(100)
    expect(fetchMock.mock.calls.some(([input]) => String(input).includes('/session/session-new'))).toBe(true)
  })

  it('subscribes before a classroom exists, activates on SESSION_STARTED, and reconnects', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: string | URL | Request) => {
      if (String(input).includes('/parent/children')) return {
        ok: true,
        json: async () => ({ children: [{ student_id: 'student-new', display_name: '媛媛', grade_level: 7, active_session_id: null, subject: null, knowledge_point: null, started_at: null }] }),
      } as Response
      return { ok: true, json: async () => liveSession('session-started') } as Response
    }))
    const store = useSupervisionStore()

    await store.initialize()
    expect(store.sessionActive).toBe(false)
    expect(FakeWebSocket.instances).toHaveLength(1)
    FakeWebSocket.instances[0]?.open()
    FakeWebSocket.instances[0]?.message({
      event_id: 'event-start', student_id: 'student-new', session_id: 'session-started', type: 'SESSION_STARTED',
      payload: { subject: '化学', knowledge_point: '物理变化与化学变化', target_minutes: 10, action: 'ASK' },
    })
    expect(store.sessionActive).toBe(true)
    expect(store.sessionID).toBe('session-started')
    expect(store.subject).toBe('化学')
    await vi.advanceTimersByTimeAsync(100)

    FakeWebSocket.instances[0]?.close()
    expect(store.reconnecting).toBe(true)
    await vi.advanceTimersByTimeAsync(500)
    expect(FakeWebSocket.instances).toHaveLength(2)
    expect(FakeWebSocket.instances[1]?.url).toContain('/ws/parent/student-new')
  })

  it('clears private parent state and ignores requests from the previous account generation', async () => {
    const children = deferred<Response>()
    vi.stubGlobal('fetch', vi.fn(() => children.promise))
    const store = useSupervisionStore()

    const initialization = store.initialize()
    store.children = [{ student_id: 'student-old', display_name: '旧账号', grade_level: 7, active_session_id: 'session-old', subject: '数学', knowledge_point: '方程', started_at: '2026-08-27T01:00:00Z' }]
    store.studentID = 'student-old'
    store.studentAnswerPreview = '旧账号回答'
    store.correctAnswer = '旧账号答案'
    store.openRealtime('student-old')
    const socket = FakeWebSocket.instances[0]
    store.reset()

    children.resolve({ ok: true, status: 200, json: async () => ({ children: [{ student_id: 'student-old', display_name: '旧账号', grade_level: 7, active_session_id: null, subject: null, knowledge_point: null, started_at: null }] }) } as Response)
    await initialization

    expect(store.children).toEqual([])
    expect(store.studentID).toBe('')
    expect(store.studentAnswerPreview).toBe('')
    expect(store.correctAnswer).toBe('')
    expect(socket?.readyState).toBe(FakeWebSocket.CLOSED)
  })
})
