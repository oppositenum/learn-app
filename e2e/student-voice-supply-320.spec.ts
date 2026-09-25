import { expect, test, type Locator, type Page } from '@playwright/test'

const sessionID = 'session-voice-320'
const sessionPath = `/api/v1/student/sessions/${sessionID}`
const voiceAudioPath = `${sessionPath}/voice/audio`

const baseSession = {
  id: sessionID,
  version: 3,
  timing_version: 1,
  subject_code: 'MATH',
  subject_name: '数学',
  knowledge_point: '分数通分',
  knowledge_point_id: 'kp-fraction',
  difficulty: 'L1',
  question_id: 'question-voice-320',
  prompt: '三分之一和四分之一的小格一样大吗？',
  scene: {},
  input_schema: {},
  started_at: '2030-01-01T00:00:00Z',
  target_minutes: 20,
  status: 'ACTIVE',
  active_seconds: 60,
  current_active_seconds: 10,
  timing_observed_at: '2030-01-01T00:01:00Z',
  socratic_round: 1,
  timeline: [],
}

const voiceSession = {
  ...baseSession,
  state: 'VOICE_EXPLAIN',
  voice_audio: voiceAudioPath,
  voice_segments: [
    { id: 'segment-1', text: '先把两个圆都分成一样多的小格。', start_ms: 0, end_ms: 3000 },
    { id: 'segment-2', text: '再比一比每一份到底有多大。', start_ms: 3000, end_ms: 6000 },
  ],
}

const supplyExplanation = '把三分之一和四分之一都换成十二分之几，小格就一样大了。'
const supplySession = {
  ...baseSession,
  state: 'EXPLAIN',
  timeline: [{ sequence: 1, actor: 'TUTOR', action: 'EXPLAIN', message: supplyExplanation, at: '2030-01-01T00:00:50Z' }],
}

const viewports = [
  { width: 320, height: 720 },
  { width: 768, height: 1024 },
]

function jsonResponse(body: unknown, status = 200) {
  return { status, contentType: 'application/json', body: JSON.stringify(body) }
}

// A short silent 8 kHz mono WAV, so the audio element has a local source.
function silentWav(milliseconds: number) {
  const samples = Math.round(8 * milliseconds)
  const wav = Buffer.alloc(44 + samples, 128)
  wav.write('RIFF', 0)
  wav.writeUInt32LE(36 + samples, 4)
  wav.write('WAVEfmt ', 8)
  wav.writeUInt32LE(16, 16)
  wav.writeUInt16LE(1, 20)
  wav.writeUInt16LE(1, 22)
  wav.writeUInt32LE(8000, 24)
  wav.writeUInt32LE(8000, 28)
  wav.writeUInt16LE(1, 32)
  wav.writeUInt16LE(8, 34)
  wav.write('data', 36)
  wav.writeUInt32LE(samples, 40)
  return wav
}

// Every API call is answered here. Anything else under /api and anything that
// leaves this machine is recorded and must stay empty.
async function routeStudent(page: Page, sessionResponse: ReturnType<typeof jsonResponse>) {
  const unexpected: string[] = []
  await page.route((url) => url.hostname !== '127.0.0.1' && url.hostname !== 'localhost', (route) => {
    unexpected.push(`${route.request().method()} ${route.request().url()}`)
    return route.abort()
  })
  // Match on the path prefix: the dev server also serves source modules under /src/api/.
  await page.route((url) => url.pathname.startsWith('/api/'), (route) => {
    unexpected.push(`${route.request().method()} ${route.request().url()}`)
    return route.fulfill(jsonResponse({ error: { message: 'not in fixture' } }, 404))
  })
  await page.route('**/api/v1/auth/me', (route) => route.fulfill(jsonResponse({
    user_id: 'user-voice-320', role: 'STUDENT', display_name: '测试学生', student_id: 'student-voice-320',
  })))
  await page.route(`**${sessionPath}`, (route) => route.fulfill(sessionResponse))
  await page.route(`**${voiceAudioPath}`, (route) => route.fulfill({ status: 200, contentType: 'audio/wav', body: silentWav(6000) }))
  return unexpected
}

async function expectReadable(locator: Locator, label: string) {
  await expect(locator).toBeVisible()
  const size = await locator.evaluate((node) => Number.parseFloat(getComputedStyle(node).fontSize))
  test.info().annotations.push({ type: 'font-size', description: `${label}: ${size}px` })
  expect(size, `${label} font-size`).toBeGreaterThanOrEqual(16)
}

// Measured only; these actions are never clicked.
async function expectTappable(locator: Locator, label: string) {
  await expect(locator).toBeVisible()
  const height = (await locator.boundingBox())!.height
  test.info().annotations.push({ type: 'height', description: `${label}: ${height}px` })
  expect(height, `${label} height`).toBeGreaterThanOrEqual(48)
  await expectReadable(locator, label)
}

async function expectNoHorizontalScroll(page: Page, width: number) {
  const scrollWidth = await page.evaluate(() => document.documentElement.scrollWidth)
  test.info().annotations.push({ type: 'scroll-width', description: `${scrollWidth}px` })
  expect(scrollWidth).toBeLessThanOrEqual(width)
}

for (const viewport of viewports) {
  test.describe(`student voice and supply at ${viewport.width}x${viewport.height}`, () => {
    test.beforeEach(async ({ page }) => {
      await page.setViewportSize(viewport)
      await page.emulateMedia({ reducedMotion: 'reduce' })
    })

    test('voice explanation text is 16px or larger and its actions are 48px tall', async ({ page }) => {
      const unexpected = await routeStudent(page, jsonResponse(voiceSession))
      await page.goto(`/student/session/${sessionID}/voice`)
      await expect(page.getByRole('heading', { name: '听完，再回原题' })).toBeVisible()
      await expect(page).toHaveURL(new RegExp(`/student/session/${sessionID}/voice$`))
      await expect(page.getByText(voiceSession.voice_segments[0].text)).toBeVisible()

      await expectReadable(page.getByText('AI 老师正在讲解'), 'AI 老师正在讲解')
      await expectReadable(page.getByText('00:00', { exact: true }), '进度时间 00:00')
      await expectReadable(page.getByText('00:06', { exact: true }), '进度时间 00:06')

      const play = page.getByRole('button', { name: '播放', exact: true })
      await expect(play).toBeEnabled()
      await expectTappable(play, '播放')
      await expectTappable(page.getByRole('button', { name: '再听一句' }), '再听一句')
      await expectTappable(page.getByRole('button', { name: '我懂了，回原题' }), '我懂了，回原题')

      await expect(page.getByTestId('voice-error')).toHaveCount(0)
      await expectNoHorizontalScroll(page, viewport.width)
      expect(unexpected).toEqual([])
    })

    test('supply explanation text is 16px or larger and its actions are 48px tall', async ({ page }) => {
      const unexpected = await routeStudent(page, jsonResponse(supplySession))
      await page.goto(`/student/session/${sessionID}/supply`)
      await expect(page.getByRole('heading', { name: '分数通分' })).toBeVisible()
      await expect(page).toHaveURL(new RegExp(`/student/session/${sessionID}/supply$`))
      await expect(page.getByText(supplyExplanation)).toBeVisible()

      await expectReadable(page.getByText('知识补给站', { exact: true }), '知识补给站')
      await expectReadable(page.getByText('当前讲解', { exact: true }), '当前讲解')

      const anotherExample = page.getByRole('button', { name: '换个例子' })
      await expect(anotherExample).toBeEnabled()
      await expectTappable(anotherExample, '换个例子')
      // The top icon link shares the "返回原题" name; the main action is the one with visible text.
      await expectTappable(page.getByRole('link', { name: '返回原题' }).filter({ hasText: '返回原题' }), '返回原题')

      await expectNoHorizontalScroll(page, viewport.width)
      expect(unexpected).toEqual([])
    })

    test('a failed voice load shows a warm 16px notice, not a red error', async ({ page }) => {
      const unexpected = await routeStudent(page, jsonResponse({ error: { message: 'unavailable' } }, 503))
      await page.goto(`/student/session/${sessionID}/voice`)

      const notice = page.getByTestId('voice-error')
      await expectReadable(notice, 'voice-error')
      const classes = (await notice.getAttribute('class'))!.split(/\s+/)
      test.info().annotations.push({ type: 'voice-error-class', description: classes.join(' ') })
      expect(classes).toContain('notice-warm')
      for (const name of classes) expect(name).not.toMatch(/red|error|wrong|danger/)

      await expectNoHorizontalScroll(page, viewport.width)
      expect(unexpected).toEqual([])
    })
  })
}
