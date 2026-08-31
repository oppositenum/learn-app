<script setup lang="ts">
import { ArrowRight, CalendarDays, Clock3, Sparkles } from '@lucide/vue'
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { getParentChildren, getParentReport, type ParentChild, type ParentReport } from '../../api/parent'

const route = useRoute()
const router = useRouter()
const children = ref<ParentChild[]>([])
const studentID = ref('')
const childName = ref('孩子')
const report = ref<ParentReport | null>(null)
const loading = ref(true)
const error = ref('')
const activeMinutes = computed(() => Math.round((report.value?.summary.active_seconds ?? 0) / 60))

async function loadReport(nextStudentID: string) {
  const child = children.value.find((item) => item.student_id === nextStudentID)
  if (!child) return
  loading.value = true
  error.value = ''
  studentID.value = child.student_id
  childName.value = child.display_name
  report.value = null
  if (route.query.student !== child.student_id) {
    await router.replace({ query: { ...route.query, student: child.student_id } })
  }
  try {
    report.value = await getParentReport(child.student_id)
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '学习报告暂时不可用'
  } finally {
    loading.value = false
  }
}

function switchChild(event: Event) {
  void loadReport((event.target as HTMLSelectElement).value)
}

onMounted(async () => {
  try {
    children.value = await getParentChildren()
    if (children.value.length === 0) throw new Error('尚未绑定孩子账户')
    const preferredID = typeof route.query.student === 'string' ? route.query.student : ''
    const child = children.value.find((item) => item.student_id === preferredID) ?? children.value[0]
    await loadReport(child.student_id)
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '学习报告暂时不可用'
    loading.value = false
  }
})
</script>

<template>
  <main class="page-wrap pb-28 pt-7">
    <p class="text-sm text-zinc-500">
      {{ childName }}的真实课堂记录
    </p>
    <h1 class="mt-1 text-2xl font-semibold">
      学习报告
    </h1>
    <label
      v-if="children.length > 1"
      class="mt-6 block text-sm font-semibold"
    >查看孩子<select
      :value="studentID"
      class="mt-2 h-11 w-full border border-zinc-300 bg-white px-3 font-normal"
      aria-label="切换报告孩子"
      @change="switchChild"
    ><option
      v-for="child in children"
      :key="child.student_id"
      :value="child.student_id"
    >{{ child.display_name }}</option></select></label>
    <p
      v-if="error"
      class="mt-6 border-l-2 border-red-600 pl-3 text-sm text-red-700"
      role="alert"
    >
      {{ error }}
    </p>
    <p
      v-else-if="loading"
      class="mt-6 text-sm text-zinc-500"
    >
      正在汇总
    </p>

    <template v-if="report">
      <section
        class="mt-8 grid grid-cols-2 border-y border-zinc-300"
        aria-label="学习汇总"
      >
        <div class="border-b border-r border-zinc-300 py-5 pr-4">
          <CalendarDays
            :size="18"
            class="text-teal-700"
          />
          <strong class="mt-3 block text-2xl tabular-nums">{{ report.summary.completed_sessions }}</strong>
          <span class="text-sm text-zinc-500">完成课堂</span>
        </div>
        <div class="border-b border-zinc-300 py-5 pl-4">
          <Clock3
            :size="18"
            class="text-sky-700"
          />
          <strong class="mt-3 block text-2xl tabular-nums">{{ activeMinutes }}</strong>
          <span class="text-sm text-zinc-500">有效分钟</span>
        </div>
        <div class="border-r border-zinc-300 py-5 pr-4">
          <Sparkles
            :size="18"
            class="text-amber-700"
          />
          <strong class="mt-3 block text-2xl tabular-nums">{{ report.summary.total_energy }}</strong>
          <span class="text-sm text-zinc-500">探索能量</span>
        </div>
        <div class="py-5 pl-4">
          <strong class="block text-2xl tabular-nums">{{ report.summary.streak_days }}</strong>
          <span class="text-sm text-zinc-500">连续活动天数</span>
        </div>
      </section>

      <section
        class="mt-9"
        aria-labelledby="activity-title"
      >
        <h2
          id="activity-title"
          class="text-lg font-semibold"
        >
          最近活动
        </h2>
        <div
          class="mt-4 grid grid-cols-7 gap-1"
          aria-label="最近十四日活动"
        >
          <div
            v-for="day in [...report.activity_days].reverse()"
            :key="day.date"
            class="grid min-h-16 place-items-center border border-zinc-200 bg-white px-1 text-center text-xs"
          >
            <span>{{ day.date.slice(5) }}</span>
            <strong>{{ Math.round(day.active_seconds / 60) }}m</strong>
          </div>
        </div>
        <p
          v-if="report.activity_days.length === 0"
          class="mt-4 text-sm text-zinc-500"
        >
          暂无完成课堂的活动记录
        </p>
      </section>

      <section
        class="mt-9"
        aria-labelledby="recent-session-title"
      >
        <h2
          id="recent-session-title"
          class="text-lg font-semibold"
        >
          最近课堂
        </h2>
        <div class="mt-4 divide-y divide-zinc-300 border-y border-zinc-300">
          <RouterLink
            v-for="session in report.recent_sessions"
            :key="session.id"
            :to="{ path: `/parent/report/session/${session.id}`, query: { student: studentID } }"
            class="group flex min-h-20 items-center justify-between gap-4 py-4 outline-none transition-colors hover:bg-white focus-visible:bg-white focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-teal-600 active:bg-zinc-100"
            :aria-label="`查看${session.subject}${session.knowledge_point}课堂详情`"
          >
            <div class="min-w-0">
              <h3 class="font-medium">
                {{ session.subject }} · {{ session.knowledge_point }}
              </h3>
              <p class="mt-1 text-sm text-zinc-500">
                {{ new Date(session.started_at).toLocaleDateString('zh-CN') }} · {{ Math.round(session.active_seconds / 60) }} 分钟
              </p>
            </div>
            <div class="flex shrink-0 items-center gap-3">
              <span class="text-sm font-semibold text-teal-800">{{ session.status === 'COMPLETED' ? '已完成' : '进行中' }}</span>
              <ArrowRight
                :size="18"
                class="text-zinc-400 transition-transform group-hover:translate-x-0.5 group-hover:text-teal-700"
                aria-hidden="true"
              />
            </div>
          </RouterLink>
          <p
            v-if="report.recent_sessions.length === 0"
            class="py-5 text-sm text-zinc-500"
          >
            暂无课堂记录
          </p>
        </div>
      </section>
    </template>
  </main>
</template>
