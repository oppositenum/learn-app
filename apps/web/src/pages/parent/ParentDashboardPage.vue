<script setup lang="ts">
import { ArrowRight, Clock3, SlidersHorizontal } from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'

import { getParentOverview, type ParentOverview, type ParentPlanProgress } from '../../api/parent'
import { useParentLiveConnection } from '../../features/supervision/useParentLiveConnection'

const learning = useParentLiveConnection()
const overview = ref<ParentOverview | null>(null)
const overviewError = ref('')
const loadedAt = ref(0)
const now = ref(performance.now())
let clockTimer: number | undefined
let refreshTimer: number | undefined
let request = 0

// The overview belongs to the selected child only.
const current = computed(() => overview.value?.student_id === learning.studentID ? overview.value : null)
const session = computed(() => current.value?.session ?? null)
const plan = computed(() => current.value?.today_plan ?? null)
const connectionLabel = computed(() => {
  if (session.value?.status === 'ACTIVE') return '正在学习'
  if (session.value?.status === 'PAUSED') return '已暂停'
  return learning.connected ? '实时已连接' : learning.reconnecting ? '正在重连' : '等待连接'
})
// Only an active classroom keeps counting; a paused one keeps its time.
const elapsed = computed(() => {
  if (!session.value) return ''
  const running = session.value.status === 'ACTIVE' ? Math.max(0, Math.floor((now.value - loadedAt.value) / 1000)) : 0
  const seconds = session.value.active_seconds + running
  return `${Math.floor(seconds / 60)} 分 ${String(seconds % 60).padStart(2, '0')} 秒`
})
const judgement = computed(() => session.value?.error_type || session.value?.misconceptions.join('、') || '还没有判断')
const progressLabels: Record<ParentPlanProgress, string> = {
  NOT_STARTED: '未开始',
  IN_PROGRESS: '进行中',
  PAUSED: '已暂停',
  COMPLETED: '已完成',
}

async function loadOverview() {
  const studentID = learning.studentID
  if (!studentID) return
  const ticket = ++request
  try {
    const next = await getParentOverview(studentID)
    if (ticket !== request) return
    overview.value = next
    loadedAt.value = performance.now()
    now.value = loadedAt.value
    overviewError.value = ''
  } catch (error) {
    if (ticket === request) overviewError.value = error instanceof Error ? error.message : '首页数据暂时不可用'
  }
}

// Every realtime message from the classroom may move the round, the time or
// the plan, so the overview is read again; the interval catches a classroom
// that went quiet without a message.
watch(() => [learning.studentID, learning.sessionID, learning.sessionActive, learning.timeline.length], () => void loadOverview())

onMounted(() => {
  void loadOverview()
  clockTimer = window.setInterval(() => { now.value = performance.now() }, 1000)
  refreshTimer = window.setInterval(() => void loadOverview(), 30000)
})
onBeforeUnmount(() => {
  window.clearInterval(clockTimer)
  window.clearInterval(refreshTimer)
})

function switchChild(event: Event) {
  void learning.selectChild((event.target as HTMLSelectElement).value)
}
</script>

<template>
  <main class="page-wrap pb-28 pt-7">
    <header class="flex items-start justify-between gap-4">
      <div>
        <p class="text-base text-zinc-500">
          {{ learning.childName || '孩子' }}的学习
        </p>
        <h1 class="mt-1 text-2xl font-semibold">
          家长监督
        </h1>
      </div>
      <span
        data-testid="parent-session-status"
        class="inline-flex shrink-0 items-center gap-2 bg-emerald-100 px-3 py-2 text-base font-semibold text-emerald-800"
      >
        <span class="size-2 bg-emerald-500" />
        {{ connectionLabel }}
      </span>
    </header>

    <label
      v-if="learning.children.length > 1"
      class="mt-6 block text-base font-semibold"
    >监督孩子<select
      :value="learning.studentID"
      class="mt-2 h-12 w-full border border-zinc-300 bg-white px-3 text-base font-normal"
      aria-label="切换监督孩子"
      @change="switchChild"
    ><option
      v-for="child in learning.children"
      :key="child.student_id"
      :value="child.student_id"
    >{{ child.display_name }} · {{ child.active_session_id ? '正在学习' : '等待开课' }}</option></select></label>

    <p
      v-if="overviewError"
      class="notice-warm mt-6 px-4 py-3 text-base font-medium"
      role="alert"
    >
      {{ overviewError }}
    </p>

    <section
      v-if="session"
      data-testid="parent-session-card"
      :data-status="session.status"
      class="mt-8 border-y border-zinc-300 py-6"
      aria-labelledby="live-title"
    >
      <div class="flex flex-wrap items-start justify-between gap-4">
        <div class="min-w-0">
          <p class="text-base text-zinc-500">
            {{ session.status === 'ACTIVE' ? '当前课堂' : '课堂已暂停' }}
          </p>
          <h2
            id="live-title"
            class="mt-1 break-words text-xl font-semibold"
          >
            {{ session.subject }} · {{ session.knowledge_point }}
          </h2>
        </div>
        <div class="text-right">
          <p
            data-testid="parent-elapsed"
            class="text-base font-semibold tabular-nums"
          >
            {{ elapsed }}
          </p>
          <p
            data-testid="parent-target"
            class="text-base text-zinc-500"
          >
            目标 {{ session.target_minutes }} 分钟
          </p>
        </div>
      </div>

      <div class="mt-5 flex items-center gap-3 bg-[#dfece8] px-4 py-3 text-teal-900">
        <Clock3
          :size="19"
          class="shrink-0"
          aria-hidden="true"
        />
        <p
          data-testid="parent-round"
          class="break-words text-base font-semibold"
        >
          {{ session.status === 'ACTIVE' ? '正在' : '停在' }}第 {{ session.socratic_round }} 轮启发 · {{ session.tutor_action }}
        </p>
      </div>

      <h3 class="mt-6 text-lg font-semibold">
        卡在哪里
      </h3>
      <dl class="mt-3 grid gap-4 sm:grid-cols-2">
        <div>
          <dt class="text-base font-semibold text-zinc-500">
            当前轮次
          </dt>
          <dd class="mt-1 text-base font-semibold">
            第 {{ session.socratic_round }} 轮
          </dd>
        </div>
        <div>
          <dt class="text-base font-semibold text-zinc-500">
            当前动作
          </dt>
          <dd
            data-testid="parent-action"
            class="mt-1 break-words text-base font-semibold"
          >
            {{ session.tutor_action }}
          </dd>
        </div>
        <div class="sm:col-span-2">
          <dt class="text-base font-semibold text-zinc-500">
            AI 判断
          </dt>
          <dd
            data-testid="parent-judgement"
            class="mt-1 break-words text-base leading-7"
          >
            {{ judgement }}
          </dd>
        </div>
      </dl>

      <RouterLink
        v-if="session.status === 'ACTIVE'"
        data-testid="parent-live-link"
        :to="{ path: '/parent/live', query: { student: learning.studentID, session: session.session_id } }"
        class="primary-button parent-action mt-6 w-full"
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
      data-testid="parent-waiting"
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
      <p class="mt-2 text-base text-zinc-500">
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
          class="icon-button parent-icon-action"
          aria-label="调整今日计划"
        >
          <SlidersHorizontal :size="19" />
        </RouterLink>
      </div>
      <ol
        v-if="plan && plan.blocks.length"
        class="mt-4 divide-y divide-zinc-300 border-y border-zinc-300"
      >
        <li
          v-for="block in plan.blocks"
          :key="block.id"
          data-testid="parent-plan-block"
          :data-progress="block.progress"
          class="flex items-center justify-between gap-3 py-3"
        >
          <div class="min-w-0">
            <p class="break-words text-base font-semibold">
              {{ block.subject }}{{ block.knowledge_point ? ` · ${block.knowledge_point}` : '' }}
            </p>
            <p class="mt-1 text-base text-zinc-500">
              {{ block.minutes }} 分钟
            </p>
          </div>
          <span
            :class="block.progress === 'COMPLETED' ? 'text-teal-800' : block.progress === 'NOT_STARTED' ? 'text-zinc-500' : 'text-amber-800'"
            class="shrink-0 text-base font-semibold"
          >{{ progressLabels[block.progress] }}</span>
        </li>
      </ol>
      <p
        v-else
        data-testid="parent-plan-empty"
        class="mt-4 text-base leading-6 text-zinc-600"
      >
        计划由系统根据当天表现生成。可调整学习时长、优先学科与复习强度。
      </p>
    </section>
  </main>
</template>
