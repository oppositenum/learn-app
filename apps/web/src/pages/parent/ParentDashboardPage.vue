<script setup lang="ts">
import { ArrowRight, Clock3, SlidersHorizontal } from '@lucide/vue'
import { computed } from 'vue'

import { useParentLiveConnection } from '../../features/supervision/useParentLiveConnection'

const learning = useParentLiveConnection()
const connectionLabel = computed(() => learning.sessionActive ? '正在学习' : learning.connected ? '实时已连接' : learning.reconnecting ? '正在重连' : '等待连接')
const answerLabel = computed(() => {
  if (learning.answerVisibility === 'SHORT_CURRENT') return learning.studentAnswerPreview
  if (learning.answerVisibility === 'WITHHELD_LONG') return '较长回答已隐藏'
  return '尚未提交简短回答'
})

function switchChild(event: Event) {
  void learning.selectChild((event.target as HTMLSelectElement).value)
}
</script>

<template>
  <main class="page-wrap pb-28 pt-7">
    <header class="flex items-start justify-between gap-4">
      <div>
        <p class="text-sm text-zinc-500">
          {{ learning.childName || '孩子' }}的学习
        </p>
        <h1 class="mt-1 text-2xl font-semibold">
          家长监督
        </h1>
      </div>
      <span class="inline-flex items-center gap-2 bg-emerald-100 px-3 py-2 text-sm font-semibold text-emerald-800">
        <span class="size-2 bg-emerald-500" />
        {{ connectionLabel }}
      </span>
    </header>

    <label
      v-if="learning.children.length > 1"
      class="mt-6 block text-sm font-semibold"
    >监督孩子<select
      :value="learning.studentID"
      class="mt-2 h-11 w-full border border-zinc-300 bg-white px-3 font-normal"
      aria-label="切换监督孩子"
      @change="switchChild"
    ><option
      v-for="child in learning.children"
      :key="child.student_id"
      :value="child.student_id"
    >{{ child.display_name }} · {{ child.active_session_id ? '正在学习' : '等待开课' }}</option></select></label>

    <section
      v-if="learning.sessionActive"
      class="mt-8 border-y border-zinc-300 py-6"
      aria-labelledby="live-title"
    >
      <div class="flex items-center justify-between gap-4">
        <div>
          <p class="text-sm text-zinc-500">
            当前课堂
          </p>
          <h2
            id="live-title"
            class="mt-1 text-xl font-semibold"
          >
            {{ learning.subject }} · {{ learning.knowledgePoint }}
          </h2>
        </div>
        <div class="text-right">
          <p class="font-semibold tabular-nums">
            {{ learning.elapsed }}
          </p>
          <p class="text-xs text-zinc-500">
            目标 {{ learning.target }}
          </p>
        </div>
      </div>
      <div class="mt-5 flex items-center gap-3 bg-[#dfece8] px-4 py-3 text-teal-900">
        <Clock3
          :size="19"
          aria-hidden="true"
        />
        <p class="text-sm font-semibold">
          正在第 {{ learning.socraticRound }} 轮启发 · {{ learning.tutorAction }}
        </p>
      </div>

      <dl class="mt-6 grid gap-5 sm:grid-cols-2">
        <div>
          <dt class="text-xs font-semibold text-zinc-500">
            孩子刚才回答
          </dt>
          <dd class="mt-1 text-lg font-semibold">
            {{ answerLabel }}
          </dd>
        </div>
        <div>
          <dt class="text-xs font-semibold text-zinc-500">
            标准答案
          </dt>
          <dd class="mt-1 text-lg font-semibold text-teal-800">
            {{ learning.correctAnswer }}
          </dd>
        </div>
        <div class="sm:col-span-2">
          <dt class="text-xs font-semibold text-zinc-500">
            AI 判断
          </dt>
          <dd class="mt-1 leading-7">
            {{ learning.misconception }}
          </dd>
        </div>
      </dl>

      <RouterLink
        :to="{ path: '/parent/live', query: { student: learning.studentID, session: learning.sessionID } }"
        class="primary-button mt-6 w-full"
      >
        查看实时课堂
        <ArrowRight
          :size="18"
          aria-hidden="true"
        />
      </RouterLink>
    </section>
    <section
      v-else
      class="mt-8 border-y border-zinc-300 py-9 text-center"
      aria-label="课堂等待状态"
    >
      <Clock3
        :size="24"
        class="mx-auto text-teal-700"
      />
      <h2 class="mt-3 text-lg font-semibold">
        等待孩子开始课堂
      </h2>
      <p class="mt-2 text-sm text-zinc-500">
        {{ learning.connected ? '实时连接已建立，开始学习后会自动显示。' : '正在建立实时连接。' }}
      </p>
    </section>

    <section
      class="mt-8"
      aria-labelledby="parent-plan-title"
    >
      <div class="flex items-center justify-between">
        <h2
          id="parent-plan-title"
          class="text-lg font-semibold"
        >
          今日计划
        </h2>
        <RouterLink
          :to="{ path: '/parent/settings', query: { student: learning.studentID } }"
          class="icon-button"
          aria-label="调整今日计划"
        >
          <SlidersHorizontal :size="19" />
        </RouterLink>
      </div>
      <p class="mt-4 text-sm leading-6 text-zinc-600">
        计划由系统根据当天表现生成。可调整学习时长、优先学科与复习强度。
      </p>
    </section>
  </main>
</template>
