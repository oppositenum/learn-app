<script setup lang="ts">
import { BookOpen, FlaskConical, Landmark, Sparkles } from '@lucide/vue'
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
        <p class="text-4xl font-semibold tabular-nums">
          {{ currentGrowth?.total_energy ?? '--' }}
        </p>
        <p class="mt-1 text-sm text-zinc-500">
          探索能量
        </p>
      </div>
      <p class="text-sm font-medium text-teal-700">
        {{ currentGrowth ? `连续 ${currentGrowth.streak_days} 天` : '等待同步' }}
      </p>
    </div>

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
  </main>
</template>
