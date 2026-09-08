import { afterEach, expect, test, vi } from 'vitest'

import { getStudentSession, requestStudentSupport, submitStudentAnswer } from './student'

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

test('maps rejected Tutor output to the answer rephrase contract by code', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => response({ code: 'TUTOR_OUTPUT_REPHRASE_REQUIRED' }, 422)))

  await expect(submitStudentAnswer('session-1', '我的想法')).rejects.toMatchObject({
    status: 422,
    code: 'TUTOR_OUTPUT_REPHRASE_REQUIRED',
    message: '刚才的回应不太合适，老师换个问法。请再提交一次',
  })
})

test('maps rejected HINT output to the hint rephrase contract by code', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => response({ code: 'TUTOR_OUTPUT_REPHRASE_REQUIRED' }, 422)))

  await expect(requestStudentSupport('session-1', 'HINT')).rejects.toMatchObject({
    status: 422,
    code: 'TUTOR_OUTPUT_REPHRASE_REQUIRED',
    message: '刚才的提示不太合适，老师换个问法。请再点一次『一点提示』',
  })
})

test('maps rejected EXPLAIN output to the explain rephrase contract by code', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => response({ code: 'TUTOR_OUTPUT_REPHRASE_REQUIRED' }, 422)))

  await expect(requestStudentSupport('session-1', 'EXPLAIN')).rejects.toMatchObject({
    status: 422,
    code: 'TUTOR_OUTPUT_REPHRASE_REQUIRED',
    message: '刚才的讲解不太合适，老师换个说法。请再点一次『我不会』',
  })
})
