<script setup lang="ts">
import { BookOpen, FlaskConical, Landmark, Sparkles } from '@lucide/vue'
import { computed, onMounted, ref } from 'vue'

import { getStudentGrowth, type StudentGrowth } from '../../api/student'

const energy = ref(0)
const streak = ref(0)
const buildings = ref<StudentGrowth['buildings']>({})
const unavailable = ref(false)
onMounted(async () => { try { const growth = await getStudentGrowth(); energy.value = growth.total_energy; streak.value = growth.streak_days; buildings.value = growth.buildings } catch { unavailable.value = true } })

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
    <div class="mt-7 flex items-end justify-between border-b border-zinc-300 pb-5">
      <div>
        <p class="text-4xl font-semibold tabular-nums">
          {{ energy }}
        </p>
        <p class="mt-1 text-sm text-zinc-500">
          探索能量
        </p>
      </div>
      <p class="text-sm font-medium text-teal-700">
        {{ unavailable ? '等待同步' : `连续 ${streak} 天` }}
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
      <div class="mt-4 space-y-3">
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
    </section>
  </main>
</template>
