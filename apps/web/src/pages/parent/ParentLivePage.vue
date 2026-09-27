<script setup lang="ts">
import { ArrowLeft, BrainCircuit, MessageCircleMore, ShieldCheck, Volume2 } from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted } from 'vue'

import { useParentLiveConnection } from '../../features/supervision/useParentLiveConnection'

const learning = useParentLiveConnection()
let refreshTimer: number | undefined

// A classroom can go quiet without a message; reading it again turns a
// classroom idle for 90 seconds into the paused view.
onMounted(() => {
  refreshTimer = window.setInterval(() => {
    if (learning.sessionID && learning.sessionStatus === 'ACTIVE') void learning.refreshSession(learning.studentID, learning.sessionID)
  }, 15000)
})
onBeforeUnmount(() => window.clearInterval(refreshTimer))

function switchChild(event: Event) {
  void learning.selectChild((event.target as HTMLSelectElement).value)
}

const supportStatus = computed(() => {
  switch (learning.tutorAction) {
    case 'VOICE_EXPLAIN': return '正在进行语音讲解'
    case 'EXPLAIN': return '已进入知识补给站'
    case 'RETURN': return '已回到原题验证'
    case 'COMPLETE': return '本次验证已完成'
    default: return '当前未进入讲解'
  }
})
const answerLabel = computed(() => {
  if (learning.answerVisibility === 'SHORT_CURRENT') return learning.studentAnswerPreview
  if (learning.answerVisibility === 'WITHHELD_LONG') return '较长回答已隐藏'
  return '尚未提交简短回答'
})

const paused = computed(() => !learning.sessionActive && learning.sessionStatus === 'PAUSED')
const statusLabel = computed(() => {
  if (learning.sessionActive) return '正在学习'
  if (paused.value) return '已暂停'
  return learning.connected ? '正在同步' : learning.reconnecting ? '正在重连' : '等待连接'
})
const emotionLabels = { CALM: '状态平稳', BORED: '有点倦', FRUSTRATED: '有点烦' } as const
const emotionLabel = computed(() => emotionLabels[learning.emotion] ?? '状态平稳')
const tutorTurn = computed(() => [...learning.timeline].reverse().find((item) => item.actor === 'TUTOR' || item.actor === 'AI')?.text ?? '')

const actorLabel = {
  AI: 'AI 老师',
  STUDENT: '孩子',
  SYSTEM: '系统分析',
  TUTOR: 'Tutor Engine',
}
</script>

<template>
  <main class="mx-auto min-h-svh w-full max-w-5xl pb-28">
    <header class="sticky top-0 z-10 flex items-center justify-between border-b border-zinc-200 bg-[#f3f4f2]/95 px-4 py-4 backdrop-blur sm:px-7">
      <RouterLink
        to="/parent"
        class="icon-button parent-icon-action"
        aria-label="返回家长首页"
      >
        <ArrowLeft :size="20" />
      </RouterLink>
      <div class="text-center">
        <h1 class="text-base font-semibold">
          实时课堂
        </h1>
        <p
          data-testid="parent-live-status"
          class="text-base text-emerald-700"
        >
          {{ statusLabel }}
        </p>
      </div>
      <span class="text-base tabular-nums text-zinc-500">{{ learning.elapsed }}</span>
    </header>

    <label
      v-if="learning.children.length > 1"
      class="mx-4 mt-5 block text-base font-semibold sm:mx-7"
    >监督孩子<select
      :value="learning.studentID"
      class="mt-2 h-12 w-full border border-zinc-300 bg-white px-3 text-base font-normal"
      aria-label="切换实时课堂孩子"
      @change="switchChild"
    ><option
      v-for="child in learning.children"
      :key="child.student_id"
      :value="child.student_id"
    >{{ child.display_name }} · {{ child.active_session_id ? '正在学习' : '等待开课' }}</option></select></label>

    <div
      v-if="learning.sessionActive"
      data-testid="parent-live-active"
      class="grid lg:grid-cols-[minmax(0,1fr)_20rem]"
    >
      <section
        class="px-4 py-6 sm:px-7 lg:border-r lg:border-zinc-200"
        aria-labelledby="timeline-title"
      >
        <p
          v-if="learning.connectionError"
          class="mb-5 border-l-2 border-red-600 pl-3 text-base text-red-700"
          role="alert"
        >
          {{ learning.connectionError }}
        </p>
        <p class="text-base text-zinc-500">
          {{ learning.subject }} · {{ learning.knowledgePoint }}
        </p>
        <h2
          id="timeline-title"
          class="mt-1 text-xl font-semibold"
        >
          课堂时间线
        </h2>
        <dl class="mt-5 space-y-4 border-y border-zinc-300 py-4">
          <div>
            <dt class="parent-live-label">
              题目
            </dt>
            <dd
              data-testid="parent-live-question"
              class="mt-1 break-words text-base font-semibold leading-7"
            >
              {{ learning.questionPrompt }}
            </dd>
          </div>
          <div>
            <dt class="parent-live-label">
              当前简短回答
            </dt>
            <dd
              data-testid="parent-live-answer"
              :data-visibility="learning.answerVisibility"
              class="mt-1 break-words text-base font-semibold"
            >
              {{ answerLabel }}
            </dd>
          </div>
          <div v-if="tutorTurn">
            <dt class="parent-live-label">
              老师这一轮
            </dt>
            <dd
              data-testid="parent-live-tutor-turn"
              class="mt-1 whitespace-pre-line break-words text-base leading-7"
            >
              {{ tutorTurn }}
            </dd>
          </div>
        </dl>
        <ol class="mt-6 space-y-6">
          <li
            v-for="item in learning.timeline"
            :key="item.id"
            class="grid grid-cols-[2rem_minmax(0,1fr)] gap-3"
          >
            <span
              class="grid size-8 place-items-center bg-white text-zinc-600"
              aria-hidden="true"
            >
              <MessageCircleMore
                v-if="item.actor === 'AI' || item.actor === 'STUDENT'"
                :size="17"
              />
              <BrainCircuit
                v-else
                :size="17"
              />
            </span>
            <div class="min-w-0 border-b border-zinc-200 pb-6">
              <div class="flex flex-wrap items-center justify-between gap-2">
                <p class="text-base font-semibold">
                  {{ actorLabel[item.actor] }}
                </p>
                <span class="text-base text-zinc-500">{{ item.meta }}</span>
              </div>
              <p class="mt-2 whitespace-pre-line leading-7 text-zinc-700">
                {{ item.text }}
              </p>
            </div>
          </li>
        </ol>
      </section>

      <aside
        class="px-4 py-6 sm:px-7"
        aria-label="教学决策详情"
      >
        <div class="flex items-center gap-2 text-base font-semibold text-teal-800">
          <ShieldCheck
            :size="18"
            aria-hidden="true"
          />
          家长可见 · 孩子端隔离
        </div>
        <dl class="mt-5 space-y-5">
          <div>
            <dt class="parent-live-label">
              标准答案
            </dt>
            <dd
              data-testid="parent-live-correct-answer"
              class="mt-1 break-words text-lg font-semibold"
            >
              {{ learning.correctAnswer }}
            </dd>
          </div>
          <div>
            <dt class="parent-live-label">
              AI 判断
            </dt>
            <dd
              data-testid="parent-live-judgement"
              class="mt-1 break-words text-base leading-7"
            >
              {{ learning.misconception }}
            </dd>
          </div>
          <div>
            <dt class="parent-live-label">
              当前教学动作
            </dt>
            <dd class="mt-1 flex flex-wrap gap-x-3 text-base font-semibold text-teal-800">
              <span data-testid="parent-live-action">{{ learning.tutorAction }}</span>
              <span data-testid="parent-live-round">第 {{ learning.socraticRound }} 轮 / 3</span>
            </dd>
          </div>
          <div class="flex flex-wrap gap-x-6 gap-y-2">
            <p
              data-testid="parent-live-hints"
              class="text-base font-semibold"
            >
              提示 {{ learning.hintCount }} 次
            </p>
            <p
              data-testid="parent-live-emotion"
              :data-emotion="learning.emotion"
              class="text-base font-semibold text-zinc-600"
            >
              {{ emotionLabel }}
            </p>
          </div>
          <div>
            <dt class="parent-live-label">
              为什么这样问
            </dt>
            <dd class="mt-1 text-base leading-7">
              {{ learning.tutorReason }}
            </dd>
          </div>
          <div>
            <dt class="parent-live-label">
              当前掌握状态
            </dt>
            <dd class="mt-2 flex items-center justify-between gap-3 text-base">
              <span class="text-zinc-600">{{ learning.masteryState }}</span>
              <span class="font-semibold tabular-nums text-teal-800">{{ learning.masteryScore.toFixed(0) }}</span>
            </dd>
          </div>
        </dl>

        <div class="mt-7 border-t border-zinc-300 pt-5">
          <div class="flex items-center gap-2 text-base font-semibold">
            <Volume2
              :size="18"
              aria-hidden="true"
            />
            {{ supportStatus }}
          </div>
          <p class="mt-2 text-base leading-6 text-zinc-500">
            第 3 轮启发后仍卡住时，系统会切换平行例子讲解，再返回原题验证。
          </p>
        </div>

        <button
          type="button"
          class="secondary-button parent-action mt-7 w-full"
          :disabled="!learning.connected"
          @click="learning.intervene('ENCOURAGEMENT')"
        >
          发送鼓励
        </button>
        <button
          type="button"
          class="secondary-button parent-action mt-3 w-full"
          :disabled="!learning.connected"
          @click="learning.intervene('REDUCE_INTENSITY')"
        >
          降低今天强度
        </button>
        <p
          class="mt-3 min-h-6 text-center text-base text-zinc-500"
          aria-live="polite"
        >
          {{ learning.interventionStatus }}
        </p>
      </aside>
    </div>
    <section
      v-else-if="paused"
      data-testid="parent-live-paused"
      class="mx-4 mt-8 border-y border-zinc-300 py-8 sm:mx-7"
      aria-labelledby="paused-title"
    >
      <p class="text-base text-zinc-500">
        {{ learning.subject }} · {{ learning.knowledgePoint }}
      </p>
      <h2
        id="paused-title"
        class="mt-1 text-xl font-semibold"
      >
        已暂停
      </h2>
      <p class="mt-2 text-base leading-7 text-zinc-600">
        孩子回到课堂后会自动继续显示。
      </p>
      <div class="mt-5 flex flex-wrap gap-x-6 gap-y-2 text-base font-semibold">
        <span data-testid="parent-live-action">{{ learning.tutorAction }}</span>
        <span data-testid="parent-live-round">第 {{ learning.socraticRound }} 轮 / 3</span>
        <span data-testid="parent-live-hints">提示 {{ learning.hintCount }} 次</span>
        <span
          data-testid="parent-live-emotion"
          :data-emotion="learning.emotion"
        >{{ emotionLabel }}</span>
      </div>
    </section>
    <section
      v-else
      data-testid="parent-live-waiting"
      class="mx-4 mt-8 border-y border-zinc-300 py-12 text-center sm:mx-7"
      aria-label="实时课堂等待状态"
    >
      <h2 class="text-lg font-semibold">
        等待孩子开始课堂
      </h2>
      <p class="mt-2 text-base text-zinc-500">
        WebSocket {{ learning.connected ? '已连接，课堂开始后会自动进入实时视图。' : '正在连接。' }}
      </p>
    </section>
  </main>
</template>
