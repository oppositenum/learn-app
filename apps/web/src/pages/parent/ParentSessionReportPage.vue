<script setup lang="ts">
import { ArrowLeft, BrainCircuit, CheckCircle2, Clock3, MessageCircleMore, XCircle } from '@lucide/vue'
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'

import { getParentChildren, getParentLiveSession, type ParentLiveSession } from '../../api/parent'
import { formatTutorReason } from '../../stores/supervision'

const route = useRoute()
const session = ref<ParentLiveSession | null>(null)
const childName = ref('孩子')
const studentID = ref('')
const loading = ref(true)
const error = ref('')

const correctAnswer = computed(() => formatAnswer(session.value?.correct_answer))
const misconception = computed(() => {
  if (session.value?.answer_correct || session.value?.error_type === 'NONE') return '本次回答未发现错误'
  return session.value?.error_type || session.value?.misconceptions?.join('、') || '本次未记录易错类型'
})
const tutorReason = computed(() => formatTutorReason(session.value?.tutor_reason ?? ''))
const durationMinutes = computed(() => {
  if (!session.value) return 0
  if (session.value.active_seconds > 0) return Math.max(1, Math.round(session.value.active_seconds / 60))
  return Math.max(1, Math.round((Date.now() - new Date(session.value.started_at).getTime()) / 60_000))
})

const actorLabel = {
  AI: 'AI 老师',
  STUDENT: '孩子',
  SYSTEM: '系统分析',
  TUTOR: 'Tutor Engine',
}

function formatAnswer(answer: unknown): string {
  if (typeof answer === 'string' || typeof answer === 'number') return String(answer)
  if (answer && typeof answer === 'object' && 'value' in answer) return String(answer.value)
  return answer == null ? '未提供' : JSON.stringify(answer)
}

onMounted(async () => {
  try {
    const children = await getParentChildren()
    if (children.length === 0) throw new Error('尚未绑定孩子账户')
    const preferredID = typeof route.query.student === 'string' ? route.query.student : ''
    const child = children.find((item) => item.student_id === preferredID) ?? children[0]
    studentID.value = child.student_id
    childName.value = child.display_name
    session.value = await getParentLiveSession(child.student_id, String(route.params.id))
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '课堂详情暂时不可用'
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <main class="page-wrap pb-28 pt-5">
    <header class="flex items-center gap-3 border-b border-zinc-300 pb-4">
      <RouterLink
        :to="{ path: '/parent/report', query: { student: studentID || undefined } }"
        class="icon-button"
        aria-label="返回学习报告"
      >
        <ArrowLeft :size="20" />
      </RouterLink>
      <div class="min-w-0">
        <p class="text-sm text-zinc-500">
          {{ childName }}的课堂记录
        </p>
        <h1 class="truncate text-xl font-semibold">
          课堂详情
        </h1>
      </div>
    </header>

    <p
      v-if="error"
      class="mt-6 border-l-2 border-red-600 pl-3 text-sm text-red-700"
      role="alert"
    >
      {{ error }}
    </p>
    <div
      v-else-if="loading"
      class="mt-7 space-y-3"
      aria-label="正在加载课堂详情"
    >
      <div class="h-5 w-2/3 animate-pulse bg-zinc-200" />
      <div class="h-24 animate-pulse bg-zinc-200" />
    </div>

    <template v-if="session">
      <section
        class="border-b border-zinc-300 py-6"
        aria-labelledby="session-summary-title"
      >
        <div class="flex items-start justify-between gap-4">
          <div class="min-w-0">
            <p class="text-sm text-zinc-500">
              {{ session.subject }} · {{ session.knowledge_point }}
            </p>
            <h2
              id="session-summary-title"
              class="mt-2 text-xl font-semibold leading-8"
            >
              {{ session.question_prompt }}
            </h2>
          </div>
          <span class="shrink-0 pt-0.5 text-sm font-semibold text-teal-800">
            {{ session.status === 'COMPLETED' ? '已完成' : '进行中' }}
          </span>
        </div>
        <p class="mt-4 flex items-center gap-2 text-sm text-zinc-500">
          <Clock3
            :size="16"
            aria-hidden="true"
          />
          {{ new Date(session.started_at).toLocaleString('zh-CN') }} · {{ durationMinutes }} 分钟
        </p>
      </section>

      <section
        class="grid border-b border-zinc-300 sm:grid-cols-2"
        aria-label="回答核对"
      >
        <div class="py-6 sm:border-r sm:border-zinc-300 sm:pr-6">
          <p class="detail-label">
            孩子最后回答
          </p>
          <p class="mt-2 text-lg font-semibold leading-8">
            {{ session.student_answer || '未提交回答' }}
          </p>
          <p
            class="mt-3 flex items-center gap-2 text-sm font-semibold"
            :class="session.answer_correct ? 'text-teal-700' : 'text-red-700'"
          >
            <CheckCircle2
              v-if="session.answer_correct"
              :size="17"
              aria-hidden="true"
            />
            <XCircle
              v-else
              :size="17"
              aria-hidden="true"
            />
            {{ session.answer_correct ? '回答正确' : '仍需巩固' }}
          </p>
        </div>
        <div class="border-t border-zinc-300 py-6 sm:border-t-0 sm:pl-6">
          <p class="detail-label">
            标准答案
          </p>
          <p class="mt-2 text-lg font-semibold leading-8 text-teal-800">
            {{ correctAnswer }}
          </p>
          <p
            v-if="session.full_solution"
            class="mt-3 text-sm leading-6 text-zinc-600"
          >
            {{ session.full_solution }}
          </p>
        </div>
      </section>

      <section
        class="border-b border-zinc-300 py-6"
        aria-labelledby="decision-title"
      >
        <div class="flex items-center gap-2">
          <BrainCircuit
            :size="19"
            aria-hidden="true"
          />
          <h2
            id="decision-title"
            class="text-lg font-semibold"
          >
            教学判断
          </h2>
        </div>
        <dl class="mt-5 grid gap-5 sm:grid-cols-2">
          <div>
            <dt class="detail-label">
              错误原因
            </dt>
            <dd class="mt-1 leading-7">
              {{ misconception }}
            </dd>
          </div>
          <div>
            <dt class="detail-label">
              当前教学动作
            </dt>
            <dd class="mt-1 font-semibold text-teal-800">
              {{ session.tutor_action || session.current_state }} · {{ session.socratic_round }}/3
            </dd>
          </div>
          <div>
            <dt class="detail-label">
              为什么这样教
            </dt>
            <dd class="mt-1 leading-7">
              {{ tutorReason || '本次未记录教学原因' }}
            </dd>
          </div>
          <div>
            <dt class="detail-label">
              掌握状态
            </dt>
            <dd class="mt-1 font-semibold">
              {{ session.mastery_state }} · {{ session.mastery_score.toFixed(0) }}
            </dd>
          </div>
        </dl>
      </section>

      <section
        class="py-6"
        aria-labelledby="session-timeline-title"
      >
        <h2
          id="session-timeline-title"
          class="text-lg font-semibold"
        >
          课堂过程
        </h2>
        <ol class="mt-5 divide-y divide-zinc-300 border-y border-zinc-300">
          <li
            v-for="item in session.timeline"
            :key="item.sequence"
            class="grid grid-cols-[2rem_minmax(0,1fr)] gap-3 py-5"
          >
            <span
              class="grid size-8 place-items-center bg-white text-zinc-600"
              aria-hidden="true"
            >
              <MessageCircleMore
                v-if="item.actor === 'STUDENT' || item.actor === 'TUTOR'"
                :size="17"
              />
              <BrainCircuit
                v-else
                :size="17"
              />
            </span>
            <div class="min-w-0">
              <div class="flex flex-wrap items-center justify-between gap-2">
                <p class="text-sm font-semibold">
                  {{ actorLabel[item.actor] ?? item.actor }}
                </p>
                <span class="text-xs text-zinc-500">{{ item.action || new Date(item.at).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' }) }}</span>
              </div>
              <p class="mt-2 leading-7 text-zinc-700">
                {{ item.message }}
              </p>
            </div>
          </li>
          <li
            v-if="session.timeline.length === 0"
            class="py-5 text-sm text-zinc-500"
          >
            暂无课堂过程记录
          </li>
        </ol>
      </section>
    </template>
  </main>
</template>
