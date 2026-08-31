import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, expect, test, vi } from 'vitest'

import { useLearningStore } from './learning'

beforeEach(() => {
  setActivePinia(createPinia())
  vi.stubGlobal('fetch', vi.fn(async () => ({
    ok: true,
    json: async () => ({
      id: 'session-voice', subject_code: 'MATH', subject_name: '数学', knowledge_point: '一元一次方程', difficulty: 'L1', question_id: 'question-1', prompt: '安全公开题目', scene: {}, input_schema: {}, started_at: '2026-08-26T12:00:00Z', target_minutes: 20, state: 'VOICE_EXPLAIN', socratic_round: 3,
      voice_audio: 'data:audio/wav;base64,UklGRg==',
      voice_segments: [{ id: 'segment-1', text: '先看平行例子。', start_ms: 0, end_ms: 1800 }],
      timeline: [{ sequence: 1, actor: 'TUTOR', action: 'VOICE_EXPLAIN', message: '先看平行例子。', at: '2026-08-26T12:01:00Z' }],
    }),
  })))
})

test('recovers persisted voice explanation after a page reload', async () => {
  const learning = useLearningStore()
  await learning.loadSession('session-voice')

  expect(learning.sessionID).toBe('session-voice')
  expect(learning.tutorAction).toBe('VOICE_EXPLAIN')
  expect(learning.voiceAudio).toBe('data:audio/wav;base64,UklGRg==')
  expect(learning.voiceSegments).toEqual([{ id: 'segment-1', text: '先看平行例子。', start_ms: 0, end_ms: 1800 }])
})
