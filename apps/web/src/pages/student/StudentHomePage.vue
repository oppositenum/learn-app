<script setup lang="ts">
import { Check, Flame, Lock, Play, RefreshCw } from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'

import { getCurrentStudentSession, getTodayPlan, startStudentSession, type PlanBlock, type StudentSession, type TodayPlan } from '../../api/student'
import { useAuthSession } from '../../stores/auth'
import { subjectNames } from '../../lib/subjects'
import { planModeLabels } from '../../lib/learningLabels'
import { normalizeLearningDate, shanghaiLearningDate, useGrowthStore } from '../../stores/growth'

const router = useRouter()
const auth = useAuthSession()
const growth = useGrowthStore()
const user = auth.user
const plan = ref<TodayPlan | null>(null)
const current = ref<StudentSession | null>(null)
const loading = ref(true)
const starting = ref('')
const error = ref('')
const loadedPlanLearningDate = ref('')
let loadSequence = 0
let activeLoad: Promise<void> | null = null
let activeLoadKey = ''
const currentGrowth = computed(() => {
	const data = growth.data
	if (!data || data.student_id !== user.value?.student_id || !loadedPlanLearningDate.value) return null
	return normalizeLearningDate(data.learning_date) === loadedPlanLearningDate.value ? data : null
})
const dateLabel = computed(() => {
	const learningDate = loadedPlanLearningDate.value || normalizeLearningDate(plan.value?.date)
	const value = learningDate ? new Date(`${learningDate}T12:00:00+08:00`) : new Date()
	return new Intl.DateTimeFormat('zh-CN', { timeZone: 'Asia/Shanghai', month: 'long', day: 'numeric', weekday: 'long' }).format(value)
})
const totalMinutes = computed(() => plan.value?.target_minutes ?? 0)
const streak = computed(() => currentGrowth.value?.streak_days)

function load(force = false): Promise<void> {
	const studentID = user.value?.student_id ?? ''
	const requestedLearningDate = shanghaiLearningDate()
	const loadKey = `${studentID}:${requestedLearningDate}`
	if (!force && activeLoad && activeLoadKey === loadKey) return activeLoad
	const sequence = ++loadSequence
	loading.value = true
	error.value = ''
	const operation = (async () => {
		try {
			for (let attempt = 0; attempt < 2; attempt++) {
				const [todayResponse, active] = await Promise.all([getTodayPlan(), getCurrentStudentSession(), growth.load(attempt > 0, studentID)])
				if (sequence !== loadSequence || studentID !== (user.value?.student_id ?? '')) return
				const today = todayResponse.plans[0] ?? null
				const planLearningDate = normalizeLearningDate(todayResponse.learning_date) || normalizeLearningDate(today?.date) || requestedLearningDate
				plan.value = today
				current.value = active
				loadedPlanLearningDate.value = planLearningDate
				const growthLearningDate = normalizeLearningDate(growth.loadedLearningDate)
				if (!growthLearningDate || growthLearningDate === planLearningDate || attempt === 1) break
			}
		} catch (cause) {
			if (sequence !== loadSequence) return
			error.value = cause instanceof Error ? cause.message : '今日计划暂时不可用'
		} finally {
			if (sequence === loadSequence) {
				loading.value = false
			}
		}
	})()
	activeLoad = operation
	activeLoadKey = loadKey
	void operation.finally(() => {
		if (activeLoad === operation) {
			activeLoad = null
			activeLoadKey = ''
		}
	})
	return operation
}


// A plan card may only present itself as the running classroom when it still
// describes it. A cross-subject MICRO_BACKTRACK block starts a session for its
// own prerequisite, but once the classroom returns to the original task the
// session serves a different subject and knowledge point while plan_block_id
// still points here. Identity is compared on subject_code and
// knowledge_point_id only: display names are not identities.
function blockMatchesCurrentSession(block: PlanBlock, session: StudentSession | null): boolean {
	if (!session) return false
	const bound = block.session_id === session.id || block.id === session.plan_block_id
	if (!bound) return false
	// Fail closed on a missing identity rather than treating it as a match.
	if (!session.subject_code || !session.knowledge_point_id) return false
	return block.subject === session.subject_code && block.knowledge_point_id === session.knowledge_point_id
}

// Bound to the running session by plan_block_id, but no longer describing it.
function blockDivergedFromCurrentSession(block: PlanBlock, session: StudentSession | null): boolean {
	if (!session) return false
	const bound = block.session_id === session.id || block.id === session.plan_block_id
	return bound && !blockMatchesCurrentSession(block, session)
}

async function begin(block: PlanBlock) {
  if (blockMatchesCurrentSession(block, current.value)) {
    await router.push(`/student/session/${current.value!.id}`)
    return
  }
  if (blockDivergedFromCurrentSession(block, current.value)) return
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

function handlePageShow(event: PageTransitionEvent) {
	if (event.persisted) {
		growth.invalidate()
		void load(true)
	}
}

const stopLearningDateWatch = watch(() => growth.loadedLearningDate, (learningDate) => {
	if (learningDate && loadedPlanLearningDate.value && learningDate !== loadedPlanLearningDate.value) void load()
})

const stopStudentWatch = watch(() => user.value?.student_id, () => {
	loadSequence++
	plan.value = null
	current.value = null
	loadedPlanLearningDate.value = ''
	void load(true)
})

onMounted(() => {
	void load()
	window.addEventListener('pageshow', handlePageShow)
})
onBeforeUnmount(() => {
	loadSequence++
	window.removeEventListener('pageshow', handlePageShow)
	stopLearningDateWatch()
	stopStudentWatch()
})
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
        :aria-label="streak === undefined ? '连续学习天数正在同步' : `连续学习 ${streak} 天`"
      >
        <Flame
          :size="18"
          fill="currentColor"
          aria-hidden="true"
        />{{ streak === undefined ? '--' : `${streak} 天` }}
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
      @click="load()"
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
        class="continue-card mt-4 flex min-h-36 flex-col justify-between p-5"
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
        class="card-flat mt-3 divide-y divide-[var(--hairline)] overflow-hidden"
      >
        <button
          v-for="block in plan.blocks"
          :key="block.id"
          type="button"
          class="flex min-h-20 w-full items-center justify-between gap-4 py-4 text-left"
          :disabled="Boolean(starting) || block.status === 'COMPLETED' || blockDivergedFromCurrentSession(block, current) || Boolean(current && block.session_id !== current.id && block.id !== current.plan_block_id)"
          @click="begin(block)"
        >
          <span class="min-w-0"><strong class="block font-medium">{{ subjectNames[block.subject] ?? block.subject }}</strong><span class="mt-1 block break-words text-sm text-zinc-500">{{ block.focus }} · {{ planModeLabels[block.mode] ?? '当前任务' }}</span></span>
          <span
            v-if="block.status === 'COMPLETED'"
            class="flex shrink-0 items-center gap-1 text-sm font-semibold text-teal-800"
          ><Check :size="17" />已完成</span>
          <span
            v-else-if="blockMatchesCurrentSession(block, current)"
            class="flex shrink-0 items-center gap-1 text-sm font-semibold text-teal-800"
          >{{ current!.status === 'PAUSED' ? '继续' : '进行中' }}<Play :size="17" /></span>
          <span
            v-else-if="blockDivergedFromCurrentSession(block, current)"
            data-testid="plan-block-diverged"
            class="flex max-w-40 shrink-0 items-center gap-1 text-right text-xs font-medium text-zinc-500"
          ><Lock :size="15" />当前课堂已回到：{{ subjectNames[current!.subject_code] ?? current!.subject_code }} · {{ current!.knowledge_point }}</span>
          <span
            v-else-if="current"
            class="flex max-w-28 shrink-0 items-center gap-1 text-right text-xs font-medium text-zinc-500"
          ><Lock :size="15" />完成当前探索后解锁</span>
          <span
            v-else
            class="flex shrink-0 items-center gap-2 text-sm font-semibold text-teal-800"
          >{{ block.minutes }} 分钟<Play :size="17" /></span>
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
