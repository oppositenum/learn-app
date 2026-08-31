<script setup lang="ts">
import { ArrowLeft, BrainCircuit, MessageCircleMore, ShieldCheck, Volume2 } from '@lucide/vue'
import { computed } from 'vue'

import { useParentLiveConnection } from '../../features/supervision/useParentLiveConnection'

const learning = useParentLiveConnection()

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
        class="icon-button"
        aria-label="返回家长首页"
      >
        <ArrowLeft :size="20" />
      </RouterLink>
      <div class="text-center">
        <h1 class="text-sm font-semibold">
          实时课堂
        </h1>
        <p class="text-xs text-emerald-700">
          {{ learning.connected ? '正在同步' : learning.reconnecting ? '正在重连' : '等待连接' }}
        </p>
      </div>
      <span class="text-sm tabular-nums text-zinc-500">{{ learning.elapsed }}</span>
    </header>

    <label
      v-if="learning.children.length > 1"
      class="mx-4 mt-5 block text-sm font-semibold sm:mx-7"
    >监督孩子<select
      :value="learning.studentID"
      class="mt-2 h-11 w-full border border-zinc-300 bg-white px-3 font-normal"
      aria-label="切换实时课堂孩子"
      @change="switchChild"
    ><option
      v-for="child in learning.children"
      :key="child.student_id"
      :value="child.student_id"
    >{{ child.display_name }} · {{ child.active_session_id ? '正在学习' : '等待开课' }}</option></select></label>

    <div
      v-if="learning.sessionActive"
      class="grid lg:grid-cols-[minmax(0,1fr)_20rem]"
    >
      <section
        class="px-4 py-6 sm:px-7 lg:border-r lg:border-zinc-200"
        aria-labelledby="timeline-title"
      >
        <p
          v-if="learning.connectionError"
          class="mb-5 border-l-2 border-red-600 pl-3 text-sm text-red-700"
          role="alert"
        >
          {{ learning.connectionError }}
        </p>
        <p class="text-sm text-zinc-500">
          {{ learning.subject }} · {{ learning.knowledgePoint }}
        </p>
        <h2
          id="timeline-title"
          class="mt-1 text-xl font-semibold"
        >
          课堂时间线
        </h2>
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
                <p class="text-sm font-semibold">
                  {{ actorLabel[item.actor] }}
                </p>
                <span class="text-xs text-zinc-500">{{ item.meta }}</span>
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
        <div class="flex items-center gap-2 text-sm font-semibold text-teal-800">
          <ShieldCheck
            :size="18"
            aria-hidden="true"
          />
          家长可见 · 孩子端隔离
        </div>
        <dl class="mt-5 space-y-5">
          <div>
            <dt class="detail-label">
              标准答案
            </dt>
            <dd class="mt-1 text-lg font-semibold">
              {{ learning.correctAnswer }}
            </dd>
          </div>
          <div>
            <dt class="detail-label">
              错误原因
            </dt>
            <dd class="mt-1 leading-7">
              {{ learning.misconception }}
            </dd>
          </div>
          <div>
            <dt class="detail-label">
              当前教学动作
            </dt>
            <dd class="mt-1 font-semibold text-teal-800">
              {{ learning.tutorAction }} · {{ learning.socraticRound }}/3
            </dd>
          </div>
          <div>
            <dt class="detail-label">
              为什么这样问
            </dt>
            <dd class="mt-1 leading-7">
              {{ learning.tutorReason }}
            </dd>
          </div>
          <div>
            <dt class="detail-label">
              当前掌握状态
            </dt>
            <dd class="mt-2 flex items-center justify-between gap-3">
              <span class="text-zinc-600">{{ learning.masteryState }}</span>
              <span class="font-semibold tabular-nums text-teal-800">{{ learning.masteryScore.toFixed(0) }}</span>
            </dd>
          </div>
        </dl>

        <div class="mt-7 border-t border-zinc-300 pt-5">
          <div class="flex items-center gap-2 text-sm font-semibold">
            <Volume2
              :size="18"
              aria-hidden="true"
            />
            {{ supportStatus }}
          </div>
          <p class="mt-2 text-sm leading-6 text-zinc-500">
            第 3 轮启发后仍卡住时，系统会切换平行例子讲解，再返回原题验证。
          </p>
        </div>

        <button
          type="button"
          class="secondary-button mt-7 w-full"
          :disabled="!learning.connected"
          @click="learning.intervene('ENCOURAGEMENT')"
        >
          发送鼓励
        </button>
        <button
          type="button"
          class="secondary-button mt-3 w-full"
          :disabled="!learning.connected"
          @click="learning.intervene('REDUCE_INTENSITY')"
        >
          降低今天强度
        </button>
        <p
          class="mt-3 min-h-5 text-center text-xs text-zinc-500"
          aria-live="polite"
        >
          {{ learning.interventionStatus }}
        </p>
      </aside>
    </div>
    <section
      v-else
      class="mx-4 mt-8 border-y border-zinc-300 py-12 text-center sm:mx-7"
      aria-label="实时课堂等待状态"
    >
      <h2 class="text-lg font-semibold">
        等待孩子开始课堂
      </h2>
      <p class="mt-2 text-sm text-zinc-500">
        WebSocket {{ learning.connected ? '已连接，课堂开始后会自动进入实时视图。' : '正在连接。' }}
      </p>
    </section>
  </main>
</template>
