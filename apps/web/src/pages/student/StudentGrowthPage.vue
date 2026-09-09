<script setup lang="ts">
import { BookOpen, CalendarCheck2, FlaskConical, HandHelping, Landmark, RefreshCcw, Shuffle, Sparkles, Target } from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted } from 'vue'

import { useGrowthStore } from '../../stores/growth'
import { useAuthSession } from '../../stores/auth'

const growth = useGrowthStore()
const auth = useAuthSession()
const currentGrowth = computed(() => growth.data?.student_id === auth.user.value?.student_id ? growth.data : null)
const buildings = computed(() => currentGrowth.value?.buildings ?? {})
function loadGrowth(force = false) {
	return growth.load(force, auth.user.value?.student_id ?? '')
}

function handlePageShow(event: PageTransitionEvent) {
	if (event.persisted) void loadGrowth(true)
}

onMounted(() => {
	void loadGrowth()
	window.addEventListener('pageshow', handlePageShow)
})
onBeforeUnmount(() => window.removeEventListener('pageshow', handlePageShow))

const count = (subject: 'MATH' | 'CHINESE' | 'ENGLISH' | 'PHYSICS' | 'CHEMISTRY') => buildings.value.mastered_by_subject?.[subject] ?? 0
const places = computed(() => [
  { name: '数学塔', detail: `${count('MATH')} 个数学知识点已点亮`, icon: Landmark, color: 'bg-teal-100 text-teal-800' },
  { name: '语言馆', detail: `${count('CHINESE') + count('ENGLISH')} 个语言知识点已点亮`, icon: BookOpen, color: 'bg-amber-100 text-amber-800' },
  { name: '科学实验室', detail: `${count('PHYSICS') + count('CHEMISTRY')} 个科学知识点 · ${buildings.value.cross_subject_insights ?? 0} 次跨学科发现`, icon: FlaskConical, color: 'bg-sky-100 text-sky-800' },
])
const evidenceIcons = {
  INDEPENDENT_SOLVING: Target,
  UNDERSTANDING_AFTER_HELP: HandHelping,
  SELF_CORRECTION: RefreshCcw,
  TRANSFER_SUCCESS: Shuffle,
  DELAYED_REVIEW: CalendarCheck2,
}
const evidence = computed(() => currentGrowth.value?.growth_evidence?.indicators ?? [])

function evidenceDetail(event: { subject: string; knowledge_point: string; occurred_at: string }) {
  return `${event.subject} · ${event.knowledge_point} · ${new Date(event.occurred_at).toLocaleDateString('zh-CN')}`
}
</script>

<template>
  <main class="page-wrap pb-28 pt-7">
    <div class="flex items-center gap-2 text-sm font-semibold text-amber-700">
      <Sparkles
        :size="18"
        aria-hidden="true"
      />
      本周成长
    </div>
    <h1 class="mt-3 text-3xl font-semibold">
      你正在建造自己的知识基地
    </h1>
    <p
      v-if="growth.error"
      class="mt-4 text-sm text-red-700"
      role="alert"
    >
      {{ growth.error }}
    </p>
    <div class="mt-7 flex items-end justify-between border-b border-zinc-300 pb-5">
      <div>
        <p class="text-sm text-zinc-500">
          学习证据
        </p>
        <p class="mt-1 text-xl font-semibold">
          五种真实进步
        </p>
      </div>
      <p class="text-sm font-medium text-teal-700">
        {{ currentGrowth ? `连续 ${currentGrowth.streak_days} 天` : '等待同步' }}
      </p>
    </div>

    <section
      class="mt-7"
      aria-labelledby="evidence-title"
    >
      <h2
        id="evidence-title"
        class="text-lg font-semibold"
      >
        成长证据
      </h2>
      <div
        v-if="currentGrowth"
        class="mt-4 divide-y divide-zinc-300 border-y border-zinc-300"
      >
        <article
          v-for="indicator in evidence"
          :key="indicator.code"
          class="grid grid-cols-[2.5rem_minmax(0,1fr)_auto] gap-3 py-4"
        >
          <span class="grid size-10 place-items-center bg-white text-teal-800">
            <component
              :is="evidenceIcons[indicator.code]"
              :size="19"
              aria-hidden="true"
            />
          </span>
          <div class="min-w-0">
            <h3 class="font-semibold">
              {{ indicator.label }}
            </h3>
            <p class="mt-1 text-sm leading-6 text-zinc-500">
              {{ indicator.events[0] ? evidenceDetail(indicator.events[0]) : '等待新的学习证据' }}
            </p>
          </div>
          <strong class="pt-1 text-2xl tabular-nums">{{ indicator.count }}</strong>
        </article>
      </div>
    </section>

    <section
      class="mt-8"
      aria-labelledby="base-title"
    >
      <h2
        id="base-title"
        class="text-lg font-semibold"
      >
        成长基地
      </h2>
      <div
        v-if="currentGrowth"
        class="mt-4 space-y-3"
      >
        <article
          v-for="place in places"
          :key="place.name"
          class="flex items-center gap-4 border border-zinc-200 bg-white p-4"
        >
          <span
            :class="place.color"
            class="grid size-12 shrink-0 place-items-center"
          >
            <component
              :is="place.icon"
              :size="22"
              aria-hidden="true"
            />
          </span>
          <div>
            <h3 class="font-semibold">
              {{ place.name }}
            </h3>
            <p class="mt-1 text-sm text-zinc-500">
              {{ place.detail }}
            </p>
          </div>
        </article>
      </div>
      <div
        v-else
        class="mt-4 space-y-3"
        aria-label="成长数据正在同步"
      >
        <div
          v-for="item in 3"
          :key="item"
          class="h-20 animate-pulse bg-zinc-200"
        />
      </div>
    </section>

    <p class="mt-8 border-t border-zinc-300 pt-4 text-xs text-zinc-500">
      兼容能量记录：{{ currentGrowth?.total_energy ?? '--' }}
    </p>
  </main>
</template>
