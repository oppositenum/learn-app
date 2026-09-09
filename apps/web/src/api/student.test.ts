import { afterEach, expect, test, vi } from 'vitest'

import { getStudentSession, requestStudentStageSupport, requestStudentSupport, submitStudentAnswer, submitStudentStageResponse } from './student'

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

test('maps generation throttling on answer submission to the child-safe busy contract', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => response({ code: 'TUTOR_GENERATION_BUSY' }, 503)))

  await expect(submitStudentAnswer('session-1', '我的想法')).rejects.toMatchObject({
    status: 503,
    code: 'TUTOR_GENERATION_BUSY',
    message: '现在有点挤，老师马上就来。请稍等一下再试一次',
  })
})

test('maps generation throttling on HINT to the same child-safe busy contract', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => response({ code: 'TUTOR_GENERATION_BUSY' }, 503)))

  await expect(requestStudentSupport('session-1', 'HINT')).rejects.toMatchObject({
    status: 503,
    code: 'TUTOR_GENERATION_BUSY',
    message: '现在有点挤，老师马上就来。请稍等一下再试一次',
  })
})

test('maps generation throttling on EXPLAIN to the same child-safe busy contract', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => response({ code: 'TUTOR_GENERATION_BUSY' }, 503)))

  await expect(requestStudentSupport('session-1', 'EXPLAIN')).rejects.toMatchObject({
    status: 503,
    code: 'TUTOR_GENERATION_BUSY',
    message: '现在有点挤，老师马上就来。请稍等一下再试一次',
  })
})

test('sends the complete stage identity and response without a free-text answer', async () => {
  const requests: Array<{ path: string; body: string }> = []
  const fetch = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    requests.push({ path: String(input), body: String(init?.body ?? '') })
    return { ok: true, status: 200, json: async () => ({}) } as Response
  })
  vi.stubGlobal('fetch', fetch)
  const identity = { stage: 'VARIANT' as const, task_id: 'task-2', task_version: 'content-v2' }

  await submitStudentStageResponse('session-1', identity, { selected_option_ids: ['a'] }, 'operation-1')
  await requestStudentStageSupport('session-1', identity, 'HINT', 'operation-2')

  expect(JSON.parse(requests[0].body)).toEqual({
    operation_id: 'operation-1', stage: 'VARIANT', task_id: 'task-2', task_version: 'content-v2',
    response: { selected_option_ids: ['a'] },
  })
  expect(JSON.parse(requests[1].body)).toEqual({
    type: 'HINT', operation_id: 'operation-2', stage: 'VARIANT', task_id: 'task-2', task_version: 'content-v2',
  })
  expect(requests[0].path).toContain('/answers')
  expect(requests[0].body).not.toContain('"answer"')
})
