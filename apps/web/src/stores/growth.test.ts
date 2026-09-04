import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { shanghaiLearningDate, useGrowthStore } from './growth'

function growth(studentID: string, learningDate: string, streakDays: number) {
  return {
    student_id: studentID,
    learning_date: learningDate,
    total_energy: streakDays * 2,
    streak_days: streakDays,
    buildings: {},
  }
}

function response(body: unknown): Response {
  return { ok: true, status: 200, json: async () => body } as Response
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((next) => { resolve = next })
  return { promise, resolve }
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.useFakeTimers()
  vi.setSystemTime(new Date('2026-08-26T15:59:00Z'))
})

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})

test('ignores a growth response from the previous student account', async () => {
  const first = deferred<Response>()
  const second = deferred<Response>()
  const fetch = vi.fn()
    .mockImplementationOnce(() => first.promise)
    .mockImplementationOnce(() => second.promise)
  vi.stubGlobal('fetch', fetch)
  const store = useGrowthStore()

  const loadingA = store.load(false, 'student-a')
  const loadingB = store.load(false, 'student-b')
  second.resolve(response(growth('student-b', '2026-08-26', 2)))
  await loadingB
  first.resolve(response(growth('student-a', '2026-08-26', 9)))
  await loadingA

  expect(store.studentID).toBe('student-b')
  expect(store.data?.student_id).toBe('student-b')
  expect(store.data?.streak_days).toBe(2)
})

test('refreshes growth after the Shanghai learning date changes', async () => {
  const fetch = vi.fn()
    .mockResolvedValueOnce(response(growth('student-a', '2026-08-26', 3)))
    .mockResolvedValueOnce(response(growth('student-a', '2026-08-27', 0)))
  vi.stubGlobal('fetch', fetch)
  const store = useGrowthStore()

  expect(shanghaiLearningDate()).toBe('2026-08-26')
  await store.load(false, 'student-a')
	await vi.advanceTimersByTimeAsync(61_000)
  expect(shanghaiLearningDate()).toBe('2026-08-27')

  expect(fetch).toHaveBeenCalledTimes(2)
  expect(store.loadedLearningDate).toBe('2026-08-27')
  expect(store.data?.streak_days).toBe(0)
})
