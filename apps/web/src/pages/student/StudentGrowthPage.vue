<script setup lang="ts">
import { BookOpen, CalendarCheck2, ChevronDown, Compass, FlaskConical, HandHelping, Landmark, Library, RefreshCcw, Shuffle, Sparkles, Target } from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

import type { GrowthEvidenceEvent, GrowthIndicatorCode } from '../../api/learning'
import { subjectCodes, subjectNames, type SubjectCode } from '../../lib/subjects'
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

const count = (subject: SubjectCode) => buildings.value.mastered_by_subject?.[subject] ?? 0
const subjects = computed(() => subjectCodes.map((code) => ({ code, name: subjectNames[code], count: count(code) })))
// Each building lights up with the knowledge points its subjects have
// mastered. No subject belongs to 世界图书馆 or 探索站 yet, so they stay at 0.
const places = computed(() => [
  { key: 'MATH_TOWER', name: '数学塔', count: count('MATH'), detail: `${count('MATH')} 个数学知识点已点亮`, icon: Landmark, color: 'bg-teal-100 text-teal-800' },
  { key: 'LANGUAGE_HALL', name: '语言馆', count: count('CHINESE') + count('ENGLISH'), detail: `${count('CHINESE') + count('ENGLISH')} 个语言知识点已点亮`, icon: BookOpen, color: 'bg-amber-100 text-amber-800' },
  { key: 'WORLD_LIBRARY', name: '世界图书馆', count: 0, detail: '0 个知识点已点亮', icon: Library, color: 'bg-rose-100 text-rose-800' },
  { key: 'SCIENCE_LAB', name: '科学实验室', count: count('PHYSICS') + count('CHEMISTRY'), detail: `${count('PHYSICS') + count('CHEMISTRY')} 个科学知识点已点亮 · ${buildings.value.cross_subject_insights ?? 0} 次跨学科发现`, icon: FlaskConical, color: 'bg-sky-100 text-sky-800' },
  { key: 'EXPLORATION_STATION', name: '探索站', count: 0, detail: '0 个知识点已点亮', icon: Compass, color: 'bg-violet-100 text-violet-800' },
])
const evidenceIcons = {
  INDEPENDENT_SOLVING: Target,
  UNDERSTANDING_AFTER_HELP: HandHelping,
  SELF_CORRECTION: RefreshCcw,
  TRANSFER_SUCCESS: Shuffle,
  DELAYED_REVIEW: CalendarCheck2,
}
const evidence = computed(() => currentGrowth.value?.growth_evidence?.indicators ?? [])
const openIndicator = ref<GrowthIndicatorCode | ''>('')

function toggleIndicator(code: GrowthIndicatorCode) {
  openIndicator.value = openIndicator.value === code ? '' : code
}

// Built from parts so every browser shows the same 8月26日 18:05 in the
// child's learning time zone.
const occurredAtParts = new Intl.DateTimeFormat('en', {
  timeZone: 'Asia/Shanghai',
  month: 'numeric',
  day: 'numeric',
  hour: '2-digit',
  minute: '2-digit',
  hourCycle: 'h23',
})

function occurredAt(value: string) {
  const part = Object.fromEntries(occurredAtParts.formatToParts(new Date(value)).map((item) => [item.type, item.value]))
  return `${part.month}月${part.day}日 ${part.hour}:${part.minute}`
}

function evidenceDetail(event: GrowthEvidenceEvent) {
  return `${subjectNames[event.subject] ?? event.subject} · ${event.knowledge_point} · ${occurredAt(event.occurred_at)}`
}
</script>

<template>
  <main class="page-wrap pb-28 pt-7">
    <div class="flex items-center gap-2 text-base font-semibold text-amber-700">
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
      data-testid="growth-error"
      class="notice-warm mt-4 px-4 py-3 text-base font-medium"
      role="alert"
    >
      {{ growth.error }}
    </p>
    <div class="mt-7 flex items-end justify-between border-b border-zinc-300 pb-5">
      <div>
        <p class="text-base text-zinc-500">
          学习证据
        </p>
        <p class="mt-1 text-xl font-semibold">
          五种真实进步
        </p>
      </div>
      <p class="text-base font-medium text-teal-700">
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
          class="py-2"
        >
          <button
            type="button"
            :data-testid="`growth-indicator-${indicator.code}`"
            :aria-expanded="openIndicator === indicator.code"
            :aria-controls="`growth-events-${indicator.code}`"
            class="growth-indicator grid min-h-12 w-full grid-cols-[2.5rem_minmax(0,1fr)_auto_1.25rem] items-center gap-3 py-2 text-left"
            @click="toggleIndicator(indicator.code)"
          >
            <span class="grid size-10 place-items-center bg-white text-teal-800">
              <component
                :is="evidenceIcons[indicator.code]"
                :size="19"
                aria-hidden="true"
              />
            </span>
            <span class="min-w-0 text-base font-semibold">{{ indicator.label }}</span>
            <strong class="text-2xl tabular-nums">{{ indicator.count }}</strong>
            <ChevronDown
              :size="20"
              class="growth-indicator-chevron text-zinc-500"
              aria-hidden="true"
            />
          </button>
          <div
            v-if="openIndicator === indicator.code"
            :id="`growth-events-${indicator.code}`"
            :data-testid="`growth-events-${indicator.code}`"
            class="growth-events pb-2 pl-[3.25rem]"
          >
            <ul
              v-if="indicator.events.length"
              class="space-y-2"
            >
              <li
                v-for="event in indicator.events"
                :key="`${event.source_kind}:${event.source_id}`"
                class="break-words text-base leading-6 text-zinc-600"
              >
                {{ evidenceDetail(event) }}
              </li>
            </ul>
            <p
              v-else
              class="text-base leading-6 text-zinc-500"
            >
              等待新的学习证据
            </p>
            <p
              v-if="indicator.events_truncated"
              data-testid="growth-events-truncated"
              class="mt-2 text-base leading-6 text-zinc-500"
            >
              还有更早的记录没有列出
            </p>
          </div>
        </article>
      </div>
    </section>

    <section
      class="mt-8"
      aria-labelledby="subjects-title"
    >
      <h2
        id="subjects-title"
        class="text-lg font-semibold"
      >
        点亮的知识点
      </h2>
      <ul
        v-if="currentGrowth"
        class="mt-4 grid grid-cols-2 gap-2 min-[375px]:grid-cols-3"
      >
        <li
          v-for="subject in subjects"
          :key="subject.code"
          :data-testid="`growth-subject-${subject.code}`"
          :data-lit="subject.count > 0"
          class="card-flat flex items-center justify-between gap-2 px-3 py-2 text-base"
        >
          <span>{{ subject.name }}</span>
          <strong
            :class="subject.count > 0 ? 'growth-lit text-teal-800' : 'text-zinc-400'"
            class="tabular-nums"
          >{{ subject.count }}</strong>
        </li>
      </ul>
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
          :key="place.key"
          :data-testid="`growth-building-${place.key}`"
          :data-lit="place.count > 0"
          class="card-flat flex items-center gap-4 p-4"
        >
          <span
            :class="place.count > 0 ? ['growth-lit', place.color] : 'bg-zinc-100 text-zinc-400'"
            class="grid size-12 shrink-0 place-items-center"
          >
            <component
              :is="place.icon"
              :size="22"
              aria-hidden="true"
            />
          </span>
          <div class="min-w-0">
            <h3 class="text-base font-semibold">
              {{ place.name }}
            </h3>
            <p class="mt-1 break-words text-base leading-6 text-zinc-500">
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

    <p
      data-testid="growth-compat-energy"
      class="mt-8 border-t border-zinc-300 pt-4 text-base text-zinc-500"
    >
      兼容能量记录：{{ currentGrowth?.total_energy ?? '--' }}
    </p>
  </main>
</template>
