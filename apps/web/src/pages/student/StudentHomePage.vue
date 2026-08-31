<script setup lang="ts">
import { Flame, Play, RefreshCw } from '@lucide/vue'
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'

import { getCurrentStudentSession, getStudentGrowth, getTodayPlan, startStudentSession, type PlanBlock, type StudentSession, type TodayPlan } from '../../api/student'
import { useAuthSession } from '../../stores/auth'
import { subjectNames } from '../../lib/subjects'

const router = useRouter()
const auth = useAuthSession()
const user = auth.user
const plan = ref<TodayPlan | null>(null)
const current = ref<StudentSession | null>(null)
const streak = ref(0)
const loading = ref(true)
const starting = ref('')
const error = ref('')
const dateLabel = new Intl.DateTimeFormat('zh-CN', { month: 'long', day: 'numeric', weekday: 'long' }).format(new Date())
const totalMinutes = computed(() => plan.value?.target_minutes ?? 0)

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [today, active, growth] = await Promise.all([getTodayPlan(), getCurrentStudentSession(), getStudentGrowth()])
    plan.value = today
    current.value = active
    streak.value = growth.streak_days
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '今日计划暂时不可用'
  } finally {
    loading.value = false
  }
}

async function begin(block: PlanBlock) {
  starting.value = block.id
  error.value = ''
  try {
    const session = await startStudentSession(block.id)
    await router.push(`/student/session/${session.id}`)
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '课堂暂时无法开始'
  } finally {
    starting.value = ''
  }
}

onMounted(load)
</script>

<template>
  <main class="page-wrap pb-28">
    <header class="flex items-start justify-between pt-7">
      <div>
        <p class="text-sm text-zinc-500">
          {{ dateLabel }}
        </p>
        <h1 class="mt-1 text-2xl font-semibold">
          你好，{{ user?.display_name }}
        </h1>
      </div>
      <div
        class="flex items-center gap-1.5 text-sm font-semibold text-amber-700"
        :aria-label="`连续学习 ${streak} 天`"
      >
        <Flame
          :size="18"
          fill="currentColor"
          aria-hidden="true"
        />{{ streak }} 天
      </div>
    </header>
    <p
      v-if="error"
      class="mt-6 border-l-2 border-red-600 pl-3 text-sm text-red-700"
      role="alert"
    >
      {{ error }}
    </p>
    <button
      v-if="error"
      type="button"
      class="secondary-button mt-3"
      @click="load"
    >
      <RefreshCw :size="17" />重试
    </button>

    <section
      v-if="current"
      class="mt-8"
      aria-labelledby="continue-title"
    >
      <p class="text-sm text-zinc-500">
        正在进行
      </p>
      <h2
        id="continue-title"
        class="mt-1 text-xl font-semibold"
      >
        继续刚才的发现
      </h2>
      <RouterLink
        :to="`/student/session/${current.id}`"
        class="mt-4 flex min-h-36 flex-col justify-between bg-zinc-900 p-5 text-white"
      >
        <div class="flex items-center justify-between">
          <span class="text-sm text-zinc-300">{{ current.subject_name }} · {{ current.knowledge_point }}</span>
          <span class="grid size-10 place-items-center bg-teal-400 text-zinc-950"><Play
            :size="19"
            fill="currentColor"
          /></span>
        </div>
        <p class="max-w-sm text-lg font-medium leading-8">
          {{ current.prompt }}
        </p>
      </RouterLink>
    </section>

    <section
      class="mt-9"
      aria-labelledby="plan-title"
    >
      <div class="flex items-center justify-between">
        <h2
          id="plan-title"
          class="text-lg font-semibold"
        >
          今日计划
        </h2>
        <span class="text-sm text-zinc-500">{{ totalMinutes }} 分钟</span>
      </div>
      <p
        v-if="loading"
        class="mt-5 text-sm text-zinc-500"
      >
        正在生成计划
      </p>
      <div
        v-else-if="plan"
        class="mt-3 divide-y divide-zinc-200 border-y border-zinc-200"
      >
        <button
          v-for="block in plan.blocks"
          :key="block.id"
          type="button"
          class="flex min-h-20 w-full items-center justify-between gap-4 py-4 text-left"
          :disabled="Boolean(starting) || Boolean(current)"
          @click="begin(block)"
        >
          <span><strong class="block font-medium">{{ subjectNames[block.subject] ?? block.subject }}</strong><span class="mt-1 block text-sm text-zinc-500">{{ block.focus }} · {{ block.mode }}</span></span>
          <span class="flex shrink-0 items-center gap-2 text-sm font-semibold text-teal-800">{{ block.minutes }} min<Play :size="17" /></span>
        </button>
      </div>
      <p
        v-else-if="!loading"
        class="mt-5 text-sm text-zinc-500"
      >
        今天没有待完成内容
      </p>
    </section>
  </main>
</template>
