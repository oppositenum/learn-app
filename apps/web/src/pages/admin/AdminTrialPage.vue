<script setup lang="ts">
import { CheckCircle2, RefreshCw } from '@lucide/vue'
import { onMounted, ref } from 'vue'

import { getOwnerTrials, type TrialRecord } from '../../api/owner'

const records = ref<TrialRecord[]>([])
const generatedAt = ref('')
const loading = ref(false)
const error = ref('')

async function load() {
  loading.value = true
  error.value = ''
  try {
    const report = await getOwnerTrials()
    records.value = report.records
    generatedAt.value = report.generated_at
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '试用报告不可用'
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>

<template>
  <main class="page-wrap pb-28 pt-7">
    <header class="flex items-end justify-between gap-4">
      <div>
        <p class="text-sm text-zinc-500">
          真实使用证据
        </p>
        <h1 class="mt-1 text-2xl font-semibold">
          七日试用
        </h1>
      </div>
      <button
        type="button"
        class="icon-button"
        aria-label="刷新试用报告"
        @click="load"
      >
        <RefreshCw :size="19" />
      </button>
    </header>

    <p class="mt-5 text-sm text-zinc-600">
      {{ generatedAt ? `更新于 ${new Date(generatedAt).toLocaleString('zh-CN')}` : '等待真实课堂记录' }}
    </p>
    <p
      v-if="error"
      class="mt-5 text-sm text-red-700"
    >
      {{ error }}
    </p>

    <div class="mt-7 divide-y divide-zinc-300 border-y border-zinc-300">
      <article
        v-for="record in records"
        :key="record.student_id"
        class="py-6"
      >
        <div class="flex items-start justify-between gap-4">
          <div>
            <h2 class="font-semibold">
              {{ record.display_name }}
            </h2>
            <p class="mt-1 text-sm text-zinc-500">
              最长活动 {{ record.longest_activity_streak_days }} 天 · 最长自愿继续 {{ record.longest_willing_streak_days }} 天
            </p>
          </div>
          <span
            v-if="record.seven_day_core_complete"
            class="inline-flex shrink-0 items-center gap-1 text-sm font-semibold text-emerald-700"
          >
            <CheckCircle2 :size="17" />已满足
          </span>
          <span
            v-else
            class="shrink-0 text-sm font-semibold text-zinc-500"
          >未满足</span>
        </div>
        <div
          class="mt-5 grid grid-cols-7 gap-1"
          aria-label="最近试用记录"
        >
          <div
            v-for="day in record.days.slice(-7)"
            :key="day.date"
            class="grid min-h-14 place-items-center border border-zinc-200 text-center text-xs"
            :class="day.willingness === 'CONTINUE_TOMORROW' ? 'bg-emerald-50 text-emerald-800' : 'bg-white text-zinc-500'"
          >
            <span>{{ day.date.slice(5) }}</span>
          </div>
        </div>
      </article>
    </div>
    <p
      v-if="!loading && records.length === 0"
      class="mt-10 text-center text-sm text-zinc-500"
    >
      暂无完成课堂的试用记录
    </p>
  </main>
</template>
