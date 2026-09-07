import { afterEach, expect, test, vi } from 'vitest'

import { getStudentSession, submitStudentAnswer } from './student'

function response(body: unknown, status: number): Response {
  return { ok: false, status, json: async () => body } as Response
}

afterEach(() => vi.restoreAllMocks())

test('keeps the existing 404 session error contract', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => response(null, 404)))

  await expect(getStudentSession('missing')).rejects.toMatchObject({
    status: 404,
    message: '学习数据暂时不可用（404）',
  })
})

test('keeps the existing 409 answer conflict contract', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => response(null, 409)))

  await expect(submitStudentAnswer('session-1', '我的想法')).rejects.toMatchObject({
    status: 409,
    message: '课堂暂时无法提交（409）',
  })
})
