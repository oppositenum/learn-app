import { expect, test, type Locator, type Page } from '@playwright/test'

// M01 sweep: every student screen and state that the delivery checklist had
// not measured yet, at 320, 375 and 768. Every API call is answered by a local
// fixture; nothing leaves this machine and nothing is written anywhere.

const viewports = [
  { width: 320, height: 720 },
  { width: 375, height: 812 },
  { width: 768, height: 1024 },
]

const forbiddenStudentText = ['标准答案', '完整解析', '家长评分', '能力低于同龄人']

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

const sessionID = 'session-sweep'
const sessionPath = `/api/v1/student/sessions/${sessionID}`
const voiceAudioPath = `${sessionPath}/voice/audio`

const baseSession = {
  id: sessionID,
  version: 2,
  timing_version: 1,
  plan_block_id: 'block-current',
  subject_code: 'MATH',
  subject_name: '数学',
  knowledge_point: '一元一次方程',
  knowledge_point_id: 'kp-equation',
  difficulty: 'L1',
  question_id: 'question-sweep',
  prompt: '小明先付 8 元，每增加 1 本练习册再付 3 元。你会怎样找出总费用？',
  scene: {},
  input_schema: {},
  started_at: '2030-01-01T00:00:00Z',
  target_minutes: 20,
  status: 'ACTIVE',
  active_seconds: 45,
  current_active_seconds: 12,
  timing_observed_at: '2030-01-01T00:00:45Z',
  state: 'ASK',
  socratic_round: 0,
  timeline: [],
}

const guidingSession = {
  ...baseSession,
  state: 'HINT',
  socratic_round: 2,
  timeline: [{ sequence: 1, actor: 'TUTOR', action: 'HINT', message: '先找找没有买练习册时要付多少。', at: '2030-01-01T00:00:40Z' }],
}

const pausedSession = { ...baseSession, status: 'PAUSED' }

const voiceSession = {
  ...baseSession,
  state: 'VOICE_EXPLAIN',
  socratic_round: 3,
  voice_audio: voiceAudioPath,
  voice_segments: [
    { id: 'segment-1', text: '先把固定要付的钱单独放在一边。', start_ms: 0, end_ms: 3000 },
    { id: 'segment-2', text: '再看每多一本会多出多少。', start_ms: 3000, end_ms: 6000 },
  ],
}

const supplySession = {
  ...baseSession,
  state: 'EXPLAIN',
  socratic_round: 3,
  timeline: [{ sequence: 1, actor: 'TUTOR', action: 'EXPLAIN', message: '把固定的钱和每本增加的钱分开写，就能看清总费用怎么变化。', at: '2030-01-01T00:00:50Z' }],
}

const today = {
  learning_date: '2030-01-01',
  plans: [{
    id: 'plan-sweep',
    date: '2030-01-01',
    target_minutes: 40,
    blocks: [
      { id: 'block-current', session_id: sessionID, sequence: 1, subject: 'MATH', knowledge_point_id: 'kp-equation', minutes: 20, mode: 'LEARN', reason: 'new', focus: '一元一次方程的实际问题', status: 'ACTIVE' },
      { id: 'block-next', sequence: 2, subject: 'ENGLISH', knowledge_point_id: 'kp-reading-detail', minutes: 10, mode: 'REVIEW', reason: 'spaced_review_due', focus: '阅读细节', status: 'AVAILABLE' },
    ],
  }],
}

const growth = {
  student_id: 'student-sweep',
  learning_date: '2030-01-01',
  total_energy: 42,
  streak_days: 7,
  buildings: {
    mastered_by_subject: { MATH: 2, CHINESE: 1, ENGLISH: 1, PHYSICS: 1, CHEMISTRY: 1 },
    cross_subject_insights: 3,
  },
  growth_evidence: {
    policy_version: 'growth-evidence-v1',
    indicators: [
      { code: 'INDEPENDENT_SOLVING', label: '独立解决', count: 8, events: [{ source_kind: 'CLASSROOM_STAGE_EVIDENCE', source_id: 'event-1', subject: 'MATH', knowledge_point: '一元一次方程在实际购物问题中的应用', occurred_at: '2030-01-01T02:00:00Z' }], events_truncated: false },
      { code: 'UNDERSTANDING_AFTER_HELP', label: '帮助后理解', count: 2, events: [], events_truncated: false },
      { code: 'SELF_CORRECTION', label: '自我纠正', count: 3, events: [], events_truncated: false },
      { code: 'TRANSFER_SUCCESS', label: '迁移成功', count: 4, events: [], events_truncated: false },
      { code: 'DELAYED_REVIEW', label: '延迟复习', count: 1, events: [], events_truncated: false },
    ],
  },
}

type Fixture = {
  today?: number
  growth?: number
  current?: unknown
  session?: { status: number, body: unknown }
  // Holds the resume request open so the classroom stays in its waiting state.
  holdResume?: boolean
  answer?: unknown
}

async function routeStudent(page: Page, fixture: Fixture) {
  const unexpected: string[] = []
  await page.route((url) => url.hostname !== '127.0.0.1' && url.hostname !== 'localhost', (route) => {
    unexpected.push(`${route.request().method()} ${route.request().url()}`)
    return route.abort()
  })
  // Registered first so every specific route below wins over it.
  await page.route((url) => url.pathname.startsWith('/api/'), (route) => {
    unexpected.push(`${route.request().method()} ${new URL(route.request().url()).pathname}`)
    return route.fulfill(jsonResponse({ error: { message: 'not in fixture' } }, 404))
  })
  await page.route('**/api/v1/auth/me', (route) => route.fulfill(jsonResponse({
    user_id: 'user-sweep', role: 'STUDENT', display_name: '测试学生', student_id: 'student-sweep',
  })))
  await page.route('**/api/v1/student/today', (route) => route.fulfill(fixture.today && fixture.today !== 200
    ? jsonResponse({ error: { message: '今日计划暂时不可用' } }, fixture.today)
    : jsonResponse(today)))
  await page.route('**/api/v1/student/growth', (route) => route.fulfill(fixture.growth && fixture.growth !== 200
    ? jsonResponse({ error: { message: 'unavailable' } }, fixture.growth)
    : jsonResponse(growth)))
  await page.route('**/api/v1/student/sessions/current', (route) => route.fulfill(fixture.current
    ? jsonResponse(fixture.current)
    : { status: 204 }))
  await page.route((url) => /^\/api\/v1\/student\/sessions\/[^/]+$/.test(url.pathname) && !url.pathname.endsWith('/current'), (route) => {
    const session = fixture.session ?? { status: 200, body: baseSession }
    return route.fulfill(jsonResponse(session.body, session.status))
  })
  await page.route('**/api/v1/student/sessions/*/heartbeat', (route) => route.fulfill(jsonResponse({
    session_id: sessionID, version: 2, timing_version: 1, status: 'ACTIVE',
    active_seconds: 45, current_active_seconds: 12, timing_observed_at: '2030-01-01T00:00:45Z',
  })))
  await page.route('**/api/v1/student/sessions/*/resume', (route) => {
    if (fixture.holdResume) return new Promise<void>(() => {})
    return route.fulfill(jsonResponse({ ...baseSession, status: 'ACTIVE' }))
  })
  await page.route('**/api/v1/student/sessions/*/answers', (route) => route.fulfill(jsonResponse(fixture.answer ?? {})))
  await page.route(`**${voiceAudioPath}`, (route) => route.fulfill({ status: 200, contentType: 'audio/wav', body: silentWav(6000) }))
  return unexpected
}

type Measurement = {
  texts: { text: string, fontSize: number }[]
  controls: { label: string, height: number, fontSize: number }[]
  scrollWidth: number
  bodyText: string
}

// Every visible element that owns text directly, and every visible control.
async function measure(page: Page): Promise<Measurement> {
  return page.evaluate(() => {
    const visible = (element: Element) => {
      const style = getComputedStyle(element)
      if (style.visibility === 'hidden' || style.display === 'none') return false
      const rect = element.getBoundingClientRect()
      // Screen-reader-only text is clipped to one pixel and is not read on screen.
      return rect.width > 1 && rect.height > 1
    }
    const texts: { text: string, fontSize: number }[] = []
    for (const element of document.body.querySelectorAll('*')) {
      if (['SCRIPT', 'STYLE', 'svg', 'path'].includes(element.tagName)) continue
      const own = [...element.childNodes]
        .filter((node) => node.nodeType === Node.TEXT_NODE)
        .map((node) => node.textContent ?? '')
        .join('')
        .trim()
      if (!own || !visible(element)) continue
      texts.push({ text: own.slice(0, 24), fontSize: Number.parseFloat(getComputedStyle(element).fontSize) })
    }
    const controls: { label: string, height: number, fontSize: number }[] = []
    for (const element of document.body.querySelectorAll('button, a[href], input:not([type="hidden"]), textarea, select, [role="button"]')) {
      if (!visible(element)) continue
      const label = (element.getAttribute('aria-label') || (element as HTMLElement).innerText || element.id || element.tagName).trim().slice(0, 24)
      // Layout rounding reports 48px targets as 47.99997px.
      controls.push({ label, height: Math.round(element.getBoundingClientRect().height * 100) / 100, fontSize: Number.parseFloat(getComputedStyle(element).fontSize) })
    }
    return { texts, controls, scrollWidth: document.documentElement.scrollWidth, bodyText: document.body.innerText }
  })
}

async function expectReadableAndTappable(page: Page, width: number, state: string) {
  const result = await measure(page)
  const smallTexts = result.texts.filter((item) => item.fontSize < 16)
  const shortControls = result.controls.filter((item) => item.height < 48)
  const minText = Math.min(...result.texts.map((item) => item.fontSize))
  const minControl = result.controls.length ? Math.min(...result.controls.map((item) => item.height)) : Number.NaN
  test.info().annotations.push({
    type: 'measure',
    description: JSON.stringify({ state, width, minText, minControl, scrollWidth: result.scrollWidth, smallTexts, shortControls }),
  })
  expect(result.texts.length, `${state} has visible text`).toBeGreaterThan(0)
  expect(smallTexts, `${state} text under 16px`).toEqual([])
  expect(shortControls, `${state} controls under 48px`).toEqual([])
  expect(result.scrollWidth, `${state} scroll width`).toBeLessThanOrEqual(width)
  for (const phrase of forbiddenStudentText) expect(result.bodyText, `${state} shows ${phrase}`).not.toContain(phrase)
}

for (const viewport of viewports) {
  test.describe(`student screens at ${viewport.width}x${viewport.height}`, () => {
    test.beforeEach(async ({ page }) => {
      await page.setViewportSize(viewport)
      await page.clock.setFixedTime(new Date('2030-01-01T04:00:00Z'))
    })

    test('home with a plan, and bottom navigation', async ({ page }) => {
      const unexpected = await routeStudent(page, { current: baseSession })
      await page.goto('/student')
      await expect(page.getByText('一元一次方程的实际问题', { exact: false })).toBeVisible()
      await expectReadableAndTappable(page, viewport.width, 'home')
      for (const name of ['首页', '成长', '我的']) {
        const item = page.locator('.nav-item', { hasText: name })
        await expect(item).toBeVisible()
      }
      expect(unexpected).toEqual([])
    })

    test('home when the plan fails to load', async ({ page }) => {
      await routeStudent(page, { today: 503 })
      await page.goto('/student')
      await expect(page.getByTestId('home-plan-error')).toBeVisible()
      await expectReadableAndTappable(page, viewport.width, 'home-plan-error')
    })

    test('growth', async ({ page }) => {
      const unexpected = await routeStudent(page, {})
      await page.goto('/student/growth')
      await expect(page.getByText('连续 7 天')).toBeVisible()
      await expectReadableAndTappable(page, viewport.width, 'growth')
      expect(unexpected).toEqual([])
    })

    test('growth when it fails to load', async ({ page }) => {
      await routeStudent(page, { growth: 503 })
      await page.goto('/student/growth')
      await expect(page.getByTestId('growth-error')).toBeVisible()
      await expectReadableAndTappable(page, viewport.width, 'growth-error')
    })

    test('profile', async ({ page }) => {
      const unexpected = await routeStudent(page, {})
      await page.goto('/student/profile')
      await expect(page.getByTestId('account-sign-out')).toBeVisible()
      await expectReadableAndTappable(page, viewport.width, 'profile')
      expect(unexpected).toEqual([])
    })

    test('classroom asking the first question', async ({ page }) => {
      const unexpected = await routeStudent(page, { session: { status: 200, body: baseSession } })
      await page.goto(`/student/session/${sessionID}`)
      await expect(page.locator('#student-answer')).toBeVisible()
      await expectReadableAndTappable(page, viewport.width, 'classroom-ask')
      expect(unexpected).toEqual([])
    })

    test('classroom guiding on the second round', async ({ page }) => {
      const unexpected = await routeStudent(page, { session: { status: 200, body: guidingSession } })
      await page.goto(`/student/session/${sessionID}`)
      await expect(page.locator('[data-tutor-turn]')).toBeVisible()
      await expectReadableAndTappable(page, viewport.width, 'classroom-guiding')
      expect(unexpected).toEqual([])
    })

    test('classroom safety notice', async ({ page }) => {
      // The answer goes only to the intercepted fixture route above.
      const unexpected = await routeStudent(page, {
        session: { status: 200, body: baseSession },
        answer: {
          session_id: sessionID, version: 3, timing_version: 1, action: 'BREAK', status: 'ACTIVE',
          message: '这个话题我们先放一放，回到题目上来。',
          active_seconds: 50, current_active_seconds: 17, timing_observed_at: '2030-01-01T00:00:50Z',
          safety: { policy_version: 'safety-v1', category: 'PERSONAL_INFORMATION', severity: 'MODERATE', fixed_action: 'REDIRECT', parent_notified: true },
        },
      })
      await page.goto(`/student/session/${sessionID}`)
      await page.locator('#student-answer').fill('测试文字')
      await page.getByRole('button', { name: '提交想法' }).click()
      await expect(page.getByTestId('safety-notice')).toBeVisible()
      await expectReadableAndTappable(page, viewport.width, 'classroom-safety')
      expect(unexpected).toEqual([])
    })

    test('classroom paused', async ({ page }) => {
      const unexpected = await routeStudent(page, { session: { status: 200, body: pausedSession } })
      await page.goto(`/student/session/${sessionID}`)
      await expect(page.getByTestId('paused-support-guidance')).toBeVisible()
      await expectReadableAndTappable(page, viewport.width, 'classroom-paused')
      expect(unexpected).toEqual([])
    })

    test('classroom whose route no longer matches the loaded session', async ({ page }) => {
      const unexpected = await routeStudent(page, { session: { status: 200, body: baseSession } })
      await page.goto('/student/session/session-older')
      await expect(page.getByTestId('session-mismatch-guidance')).toBeVisible()
      await expectReadableAndTappable(page, viewport.width, 'classroom-mismatch')
      expect(unexpected).toEqual([])
    })

    test('classroom waiting for the tutor', async ({ page }) => {
      // Resuming a paused classroom is held open, which keeps the classroom in
      // the same waiting state a support request shows, without asking for one.
      const unexpected = await routeStudent(page, { session: { status: 200, body: pausedSession }, holdResume: true })
      await page.goto(`/student/session/${sessionID}`)
      await page.getByTestId('resume-session').click()
      await expect(page.getByTestId('support-waiting')).toBeVisible()
      await expectReadableAndTappable(page, viewport.width, 'classroom-waiting')
      expect(unexpected).toEqual([])
    })

    test('voice explanation while playing', async ({ page }) => {
      const unexpected = await routeStudent(page, { session: { status: 200, body: voiceSession } })
      await page.goto(`/student/session/${sessionID}/voice`)
      await expect(page.getByRole('heading', { name: '听完，再回原题' })).toBeVisible()
      await page.getByRole('button', { name: '播放', exact: true }).click()
      await expect(page.getByRole('button', { name: '暂停', exact: true })).toBeVisible()
      await expectReadableAndTappable(page, viewport.width, 'voice-playing')
      expect(unexpected).toEqual([])
    })

    test('voice explanation when it fails to load', async ({ page }) => {
      await routeStudent(page, { session: { status: 503, body: { error: { message: '课堂暂时不可用' } } } })
      await page.goto(`/student/session/${sessionID}/voice`)
      await expect(page.getByTestId('voice-error')).toBeVisible()
      await expectReadableAndTappable(page, viewport.width, 'voice-error')
    })

    test('supply station in progress', async ({ page }) => {
      const unexpected = await routeStudent(page, { session: { status: 200, body: supplySession } })
      await page.goto(`/student/session/${sessionID}/supply`)
      await expect(page.getByText(supplySession.timeline[0].message)).toBeVisible()
      await expectReadableAndTappable(page, viewport.width, 'supply-active')
      expect(unexpected).toEqual([])
    })

    test('supply station when it fails to load', async ({ page }) => {
      await routeStudent(page, { session: { status: 503, body: { error: { message: '课堂暂时不可用' } } } })
      await page.goto(`/student/session/${sessionID}/supply`)
      const notice = page.getByTestId('supply-error')
      await expect(notice).toBeVisible()
      const classes = (await notice.getAttribute('class'))!.split(/\s+/)
      expect(classes).toContain('notice-warm')
      for (const name of classes) expect(name).not.toMatch(/red|error|wrong|danger/)
      await expectReadableAndTappable(page, viewport.width, 'supply-error')
    })
  })
}

test.describe('student motion', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 720 })
    await page.clock.setFixedTime(new Date('2030-01-01T04:00:00Z'))
  })

  // Holds the pointer down on 重试 while `check` reads the pressed state, then
  // releases away from the button so the press never becomes a click.
  async function whilePressed(page: Page, check: (button: Locator) => Promise<void>) {
    const retry = page.getByRole('button', { name: '重试' })
    await expect(retry).toBeVisible()
    const box = (await retry.boundingBox())!
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2)
    await page.mouse.down()
    try {
      await check(retry)
    } finally {
      await page.mouse.move(0, 0)
      await page.mouse.up()
    }
  }

  const transformOf = (button: Locator) => button.evaluate((node) => getComputedStyle(node).transform)

  test('a pressed button scales over 150ms when motion is allowed', async ({ page }) => {
    await routeStudent(page, { today: 503 })
    await page.goto('/student')
    const retry = page.getByRole('button', { name: '重试' })
    await expect(retry).toBeVisible()
    const timing = await retry.evaluate((node) => {
      const style = getComputedStyle(node)
      const properties = style.transitionProperty.split(',').map((item) => item.trim())
      const durations = style.transitionDuration.split(',').map((item) => item.trim())
      return Object.fromEntries(properties.map((property, index) => [property, durations[index % durations.length]]))
    })
    test.info().annotations.push({ type: 'transition', description: JSON.stringify(timing) })
    expect(timing.transform).toBe('0.15s')
    for (const duration of Object.values(timing)) {
      const ms = Number.parseFloat(duration) * 1000
      expect(ms).toBeGreaterThanOrEqual(150)
      expect(ms).toBeLessThanOrEqual(400)
    }
    await whilePressed(page, async (button) => {
      await expect.poll(() => transformOf(button)).toBe('matrix(0.985, 0, 0, 0.985, 0, 0)')
      test.info().annotations.push({ type: 'pressed-transform', description: await transformOf(button) })
    })
  })

  test('reduced motion stops the press scale and the tutor turn entrance', async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await routeStudent(page, { today: 503 })
    await page.goto('/student')
    await whilePressed(page, async (button) => {
      expect(await page.evaluate(() => matchMedia('(prefers-reduced-motion: reduce)').matches)).toBe(true)
      const transform = await transformOf(button)
      test.info().annotations.push({ type: 'pressed-transform', description: transform })
      expect(transform).toBe('none')
    })

    await routeStudent(page, { session: { status: 200, body: guidingSession } })
    await page.goto(`/student/session/${sessionID}`)
    const turn = page.locator('[data-tutor-turn]')
    await expect(turn).toBeVisible()
    const animation = await turn.evaluate((node) => getComputedStyle(node).animationName)
    test.info().annotations.push({ type: 'tutor-turn-animation', description: animation })
    expect(animation).toBe('none')
  })
})
