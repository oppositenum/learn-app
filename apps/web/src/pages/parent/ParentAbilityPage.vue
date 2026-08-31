<script setup lang="ts">
import { BrainCircuit, CircleAlert } from '@lucide/vue'
import { onMounted, ref } from 'vue'

import { getParentAbility, getParentChildren, type ParentAbility } from '../../api/parent'

const childName = ref('孩子')
const ability = ref<ParentAbility | null>(null)
const loading = ref(true)
const error = ref('')

onMounted(async () => {
  try {
    const child = (await getParentChildren())[0]
    if (!child) throw new Error('尚未绑定孩子账户')
    childName.value = child.display_name
    ability.value = await getParentAbility(child.student_id)
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '能力数据暂时不可用'
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <main class="page-wrap pb-28 pt-7">
    <p class="text-sm text-zinc-500">
      {{ childName }}的学习证据
    </p>
    <h1 class="mt-1 text-2xl font-semibold">
      能力进展
    </h1>
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

    <section
      v-if="ability"
      class="mt-8"
      aria-labelledby="subject-ability-title"
    >
      <h2
        id="subject-ability-title"
        class="text-lg font-semibold"
      >
        五科学习状态
      </h2>
      <div class="mt-4 divide-y divide-zinc-300 border-y border-zinc-300">
        <article
          v-for="subject in ability.subjects"
          :key="subject.code"
          class="py-5"
        >
          <div class="flex items-center justify-between gap-4">
            <div class="min-w-0">
              <h3 class="font-semibold">
                {{ subject.name }}
              </h3>
              <p class="mt-1 text-sm text-zinc-500">
                已接触 {{ subject.started_knowledge_points }} · 已理解 {{ subject.understood_knowledge_points }} · 已掌握 {{ subject.mastered_knowledge_points }}
              </p>
            </div>
            <strong class="shrink-0 text-xl tabular-nums text-teal-800">{{ subject.average_score.toFixed(0) }}</strong>
          </div>
          <div class="mt-3 h-1.5 bg-zinc-200">
            <div
              class="h-full bg-teal-600"
              :style="{ width: `${Math.min(100, Math.max(0, subject.average_score))}%` }"
            />
          </div>
        </article>
      </div>
    </section>

    <section
      v-if="ability?.core_abilities.length"
      class="mt-9"
      aria-labelledby="core-ability-title"
    >
      <div class="flex items-center gap-2">
        <BrainCircuit :size="19" />
        <h2
          id="core-ability-title"
          class="text-lg font-semibold"
        >
          通用能力证据
        </h2>
      </div>
      <dl class="mt-4 grid gap-3 sm:grid-cols-2">
        <div
          v-for="item in ability.core_abilities"
          :key="item.code"
          class="border-b border-zinc-300 py-3"
        >
          <dt class="font-medium">
            {{ item.name }}
          </dt>
          <dd class="mt-1 text-sm text-zinc-500">
            {{ item.evidence_count }} 条证据 · {{ item.score.toFixed(0) }} 分
          </dd>
        </div>
      </dl>
    </section>

    <section
      v-if="ability"
      class="mt-9"
      aria-labelledby="misconception-title"
    >
      <div class="flex items-center gap-2">
        <CircleAlert :size="19" />
        <h2
          id="misconception-title"
          class="text-lg font-semibold"
        >
          正在复习的易错点
        </h2>
      </div>
      <div class="mt-4 divide-y divide-zinc-300 border-y border-zinc-300">
        <article
          v-for="item in ability.misconceptions"
          :key="`${item.code}-${item.knowledge_point}`"
          class="py-4"
        >
          <h3 class="font-medium">
            {{ item.name }}
          </h3>
          <p class="mt-1 text-sm text-zinc-500">
            {{ item.subject }} · {{ item.knowledge_point }} · 出现 {{ item.occurrences }} 次 · 已纠正 {{ item.successful_corrections }} 次
          </p>
        </article>
        <p
          v-if="ability.misconceptions.length === 0"
          class="py-5 text-sm text-zinc-500"
        >
          暂无需要持续追踪的易错点
        </p>
      </div>
    </section>
  </main>
</template>
