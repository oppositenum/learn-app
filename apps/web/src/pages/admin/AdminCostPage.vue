<script setup lang="ts">
import { RefreshCw } from '@lucide/vue'
import { onMounted, reactive, ref } from 'vue'
import { getOwnerCosts, type CostRecord, type CostSummary } from '../../api/owner'
const records=ref<CostRecord[]>([]);const error=ref('');const summary=ref<CostSummary>({total_cost_usd:'0',cost_per_active_student_day_usd:'0',cost_per_20_minute_lesson_usd:'0',cost_per_mastered_skill_usd:'0',cached_ratio:'0',stt_cost_usd:'0',tts_cost_usd:'0',strong_model_ratio:'0',average_tokens_per_request:'0'})
const filters=reactive({student_id:'',session_id:'',subject:'',model:'',purpose:'',date_from:'',date_to:''})
async function load(){error.value='';try{const result=await getOwnerCosts(filters);records.value=result.records;summary.value=result.summary}catch(cause){error.value=cause instanceof Error?cause.message:'成本数据不可用'}}onMounted(load)
</script>
<template>
  <main class="page-wrap pb-28 pt-7">
    <header class="flex items-end justify-between gap-4">
      <div>
        <p class="text-sm text-zinc-500">
          逐请求计量
        </p><h1 class="mt-1 text-2xl font-semibold">
          AI 与语音成本
        </h1>
      </div><button
        type="button"
        class="icon-button"
        aria-label="刷新成本"
        @click="load"
      >
        <RefreshCw :size="19" />
      </button>
    </header><form
      class="mt-6 grid gap-3 border-y border-zinc-300 py-5 sm:grid-cols-3"
      @submit.prevent="load"
    >
      <input
        v-model="filters.student_id"
        class="h-10 border border-zinc-300 bg-white px-3 text-sm"
        placeholder="Student UUID"
      >
      <input
        v-model="filters.session_id"
        class="h-10 border border-zinc-300 bg-white px-3 text-sm"
        placeholder="Session UUID"
      >
      <select
        v-model="filters.subject"
        class="h-10 border border-zinc-300 bg-white px-3 text-sm"
      >
        <option value="">
          全部学科
        </option><option
          v-for="code in ['MATH','CHINESE','ENGLISH','PHYSICS','CHEMISTRY']"
          :key="code"
          :value="code"
        >
          {{ code }}
        </option>
      </select>
      <input
        v-model="filters.model"
        class="h-10 border border-zinc-300 bg-white px-3 text-sm"
        placeholder="模型"
      >
      <input
        v-model="filters.purpose"
        class="h-10 border border-zinc-300 bg-white px-3 text-sm"
        placeholder="Purpose"
      >
      <div class="flex gap-2">
        <input
          v-model="filters.date_from"
          type="date"
          class="h-10 min-w-0 flex-1 border border-zinc-300 bg-white px-2 text-sm"
        ><input
          v-model="filters.date_to"
          type="date"
          class="h-10 min-w-0 flex-1 border border-zinc-300 bg-white px-2 text-sm"
        >
      </div>
      <button
        type="submit"
        class="primary-button sm:col-span-3"
      >
        应用筛选
      </button>
    </form><div class="grid grid-cols-2 border-b border-zinc-300 py-5 sm:grid-cols-4">
      <div><strong class="text-2xl tabular-nums">${{ Number(summary.total_cost_usd).toFixed(4) }}</strong><span class="mt-1 block text-xs text-zinc-500">成本合计</span></div>
      <div><strong class="text-2xl tabular-nums">${{ Number(summary.cost_per_active_student_day_usd).toFixed(4) }}</strong><span class="mt-1 block text-xs text-zinc-500">学生 / 日</span></div>
      <div><strong class="text-2xl tabular-nums">{{ (Number(summary.cached_ratio)*100).toFixed(0) }}%</strong><span class="mt-1 block text-xs text-zinc-500">缓存输入</span></div>
      <div><strong class="text-2xl tabular-nums">{{ (Number(summary.strong_model_ratio)*100).toFixed(0) }}%</strong><span class="mt-1 block text-xs text-zinc-500">强模型占比</span></div>
    </div><div class="mt-4 grid grid-cols-2 gap-y-3 text-sm sm:grid-cols-4">
      <span>20 分钟 ${{ Number(summary.cost_per_20_minute_lesson_usd).toFixed(4) }}</span><span>掌握知识点 ${{ Number(summary.cost_per_mastered_skill_usd).toFixed(4) }}</span><span>STT ${{ Number(summary.stt_cost_usd).toFixed(4) }}</span><span>TTS ${{ Number(summary.tts_cost_usd).toFixed(4) }}</span>
    </div><p
      v-if="error"
      class="mt-6 text-sm text-red-700"
    >
      {{ error }}
    </p><div class="mt-7 overflow-x-auto">
      <table class="w-full min-w-[620px] border-collapse text-left text-sm">
        <thead class="border-b border-zinc-300 text-xs text-zinc-500">
          <tr>
            <th class="py-3">
              日期
            </th><th>模型 / purpose</th><th>请求</th><th>Token</th><th>语音秒</th><th class="text-right">
              成本 USD
            </th>
          </tr>
        </thead><tbody class="divide-y divide-zinc-200">
          <tr
            v-for="item in records"
            :key="`${item.date}-${item.model}-${item.purpose}`"
          >
            <td class="py-4">
              {{ item.date }}
            </td><td><strong class="block">{{ item.model }}</strong><small class="text-zinc-500">{{ item.purpose }}</small></td><td>{{ item.requests }}</td><td>{{ item.input_tokens+item.output_tokens }}</td><td>{{ item.audio_input_seconds+item.audio_output_seconds }}</td><td class="text-right tabular-nums">
              {{ item.estimated_cost_usd }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </main>
</template>
