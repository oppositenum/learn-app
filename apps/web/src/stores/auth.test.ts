import { createPinia, setActivePinia } from 'pinia'
import { afterEach, expect, test, vi } from 'vitest'

import { useAuthSession } from './auth'
import { useGrowthStore } from './growth'
import { useLearningStore } from './learning'
import { useSupervisionStore } from './supervision'

function response(body: unknown, status = 200): Response {
  return { ok: status >= 200 && status < 300, status, json: async () => body } as Response
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((next) => { resolve = next })
  return { promise, resolve }
}

afterEach(() => {
  useAuthSession().resetForTests()
  vi.restoreAllMocks()
})

test('clears student-domain stores when the account logs out', async () => {
  setActivePinia(createPinia())
  vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, status: 204 } as Response)))
  const auth = useAuthSession()
  const growth = useGrowthStore()
  const learning = useLearningStore()
  const supervision = useSupervisionStore()
  auth.user.value = { user_id: 'user-1', role: 'STUDENT', display_name: '学生', student_id: 'student-1' }
  growth.studentID = 'student-1'
  growth.data = { student_id: 'student-1', learning_date: '2026-08-26', total_energy: 12, streak_days: 3, buildings: {} }
  learning.sessionID = 'session-1'
  learning.status = 'ACTIVE'
  supervision.children = [{ student_id: 'child-1', display_name: '孩子甲', grade_level: 7, active_session_id: 'session-child', subject: '数学', knowledge_point: '方程', started_at: '2026-08-26T12:00:00Z' }]
  supervision.studentAnswer = '旧家长可见的回答'

  await auth.logout()

  expect(auth.user.value).toBeNull()
  expect(growth.studentID).toBe('')
  expect(growth.data).toBeNull()
  expect(learning.sessionID).toBe('')
  expect(learning.status).toBe('')
  expect(supervision.children).toEqual([])
  expect(supervision.studentAnswer).toBe('')
})

test('refreshes a changed student identity and clears the previous student stores', async () => {
  setActivePinia(createPinia())
  vi.stubGlobal('fetch', vi.fn(async () => response({ user_id: 'user-2', role: 'STUDENT', display_name: '学生乙', student_id: 'student-2' })))
  const auth = useAuthSession()
  const growth = useGrowthStore()
  const learning = useLearningStore()
  const supervision = useSupervisionStore()
  auth.user.value = { user_id: 'user-1', role: 'STUDENT', display_name: '学生甲', student_id: 'student-1' }
  growth.studentID = 'student-1'
  growth.data = { student_id: 'student-1', learning_date: '2026-08-26', total_energy: 12, streak_days: 3, buildings: {} }
  learning.sessionID = 'session-1'
  learning.status = 'ACTIVE'
  supervision.children = [{ student_id: 'child-1', display_name: '孩子甲', grade_level: 7, active_session_id: 'session-child', subject: '数学', knowledge_point: '方程', started_at: '2026-08-26T12:00:00Z' }]
  supervision.correctAnswer = '旧账号私有答案'

  await auth.refresh()

  expect(auth.user.value?.user_id).toBe('user-2')
  expect(growth.studentID).toBe('')
  expect(growth.data).toBeNull()
  expect(learning.sessionID).toBe('')
  expect(learning.status).toBe('')
  expect(supervision.children).toEqual([])
  expect(supervision.correctAnswer).toBe('')
})

test('does not let an older refresh restore a logged-out identity', async () => {
  setActivePinia(createPinia())
  const refresh = deferred<Response>()
  vi.stubGlobal('fetch', vi.fn((input: string | URL | Request) => {
    if (String(input).endsWith('/auth/me')) return refresh.promise
    return Promise.resolve(response(null, 204))
  }))
  const auth = useAuthSession()
  auth.user.value = { user_id: 'user-1', role: 'STUDENT', display_name: '学生甲', student_id: 'student-1' }

  const pendingRefresh = auth.refresh()
  await auth.logout()
  refresh.resolve(response({ user_id: 'user-1', role: 'STUDENT', display_name: '学生甲', student_id: 'student-1' }))
  await pendingRefresh

  expect(auth.user.value).toBeNull()
})

test('does not let an older refresh overwrite a newer login', async () => {
  setActivePinia(createPinia())
  const refresh = deferred<Response>()
  vi.stubGlobal('fetch', vi.fn((input: string | URL | Request) => {
    if (String(input).endsWith('/auth/me')) return refresh.promise
    return Promise.resolve(response({ user_id: 'user-2', role: 'STUDENT', display_name: '学生乙', student_id: 'student-2' }))
  }))
  const auth = useAuthSession()
  auth.user.value = { user_id: 'user-1', role: 'STUDENT', display_name: '学生甲', student_id: 'student-1' }

  const pendingRefresh = auth.refresh()
  await auth.login('student-b@example.test', 'password')
  refresh.resolve(response({ user_id: 'user-1', role: 'STUDENT', display_name: '学生甲', student_id: 'student-1' }))
  await pendingRefresh

  expect(auth.user.value?.user_id).toBe('user-2')
  expect(auth.user.value?.student_id).toBe('student-2')
})
