<script setup lang="ts">
import {
  CircleCheck,
  CircleX,
  LoaderCircle,
  RefreshCw,
  ScanSearch,
  Send,
  ShieldCheck,
  ShieldX,
  Sparkles,
} from '@lucide/vue'
import { computed, onMounted, reactive, ref, watch } from 'vue'

import {
  generateContentDrafts,
  getContentGenerationOptions,
  getOwnerContent,
  importContentDraft,
  quarantineContent,
  releaseContent,
  reviewContent,
  validateContent,
  type ContentGenerationOptions,
  type ContentKnowledgePointBreakdown,
  type ContentRecord,
  type GenerateContentInput,
  type GeneratedContentDraft,
} from '../../api/owner'

const emptyOptions: ContentGenerationOptions = {
  generator_available: false,
  knowledge_points: [],
  sources: [],
}

const records = ref<ContentRecord[]>([])
const breakdowns = ref<ContentKnowledgePointBreakdown[]>([])
const options = ref<ContentGenerationOptions>(emptyOptions)
const error = ref('')
const loading = ref(false)
const generating = ref(false)
const acting = ref('')
const importJSON = ref('')
const reasons = ref<Record<string, string>>({})
const selectedSubject = ref('')
const selectedGradeBand = ref('')
const selectedDomain = ref('')
const knowledgeSearch = ref('')
const lastGenerated = ref<GeneratedContentDraft[]>([])

const generation = reactive<GenerateContentInput>({
  knowledge_point_id: '',
  source_id: '',
  difficulty: 'L2',
  question_type: 'FREE_TEXT',
  count: 1,
  requirements: '',
})

const released = computed(() => records.value.filter((item) => item.status === 'RELEASED').length)
const subjects = computed(() => {
  const unique = new Map<string, string>()
  for (const item of options.value.knowledge_points) unique.set(item.subject_code, item.subject_name)
  return [...unique].map(([code, name]) => ({ code, name }))
})
const gradeBands = computed(() => {
  const unique = new Map<string, string>()
  for (const item of options.value.knowledge_points) {
    if (item.subject_code === selectedSubject.value) unique.set(item.grade_band_code, item.grade_band_name)
  }
  return [...unique].map(([code, name]) => ({ code, name }))
})
const domains = computed(() => {
  const unique = new Map<string, { code: string; name: string; knowledgePointCount: number }>()
  for (const item of options.value.knowledge_points) {
    if (item.subject_code === selectedSubject.value && item.grade_band_code === selectedGradeBand.value) {
      const current = unique.get(item.domain_code)
      if (current) current.knowledgePointCount += 1
      else unique.set(item.domain_code, { code: item.domain_code, name: item.domain_name, knowledgePointCount: 1 })
    }
  }
  return [...unique.values()]
})
const filteredKnowledgePoints = computed(() => {
  const query = knowledgeSearch.value.trim().toLocaleLowerCase()
  return options.value.knowledge_points.filter((item) => {
    if (item.subject_code !== selectedSubject.value || item.grade_band_code !== selectedGradeBand.value || item.domain_code !== selectedDomain.value) return false
    if (!query) return true
    return [item.knowledge_point_code, item.name, item.unit_name, item.domain_name, item.description]
      .some((value) => value.toLocaleLowerCase().includes(query))
  })
})
const selectedKnowledgePoint = computed(() => options.value.knowledge_points.find((item) => item.id === generation.knowledge_point_id))

function selectValidGradeBand() {
  if (!gradeBands.value.some((item) => item.code === selectedGradeBand.value)) selectedGradeBand.value = gradeBands.value[0]?.code ?? ''
}

function selectValidDomain() {
  if (!domains.value.some((item) => item.code === selectedDomain.value)) selectedDomain.value = domains.value[0]?.code ?? ''
}

function selectFirstKnowledgePoint() {
  const currentIsVisible = filteredKnowledgePoints.value.some((item) => item.id === generation.knowledge_point_id)
  if (!currentIsVisible) generation.knowledge_point_id = filteredKnowledgePoints.value[0]?.id ?? ''
}

function applyGenerationDefaults() {
  if (!subjects.value.some((item) => item.code === selectedSubject.value)) selectedSubject.value = subjects.value[0]?.code ?? ''
  selectValidGradeBand()
  selectValidDomain()
  selectFirstKnowledgePoint()
  if (!options.value.sources.some((item) => item.id === generation.source_id)) generation.source_id = options.value.sources[0]?.id ?? ''
}

watch(selectedSubject, () => {
  knowledgeSearch.value = ''
  selectValidGradeBand()
  selectValidDomain()
  selectFirstKnowledgePoint()
})
watch(selectedGradeBand, () => {
  knowledgeSearch.value = ''
  selectValidDomain()
  selectFirstKnowledgePoint()
})
watch([selectedDomain, knowledgeSearch], selectFirstKnowledgePoint)
watch(
  () => generation.knowledge_point_id,
  (id) => {
    const selected = options.value.knowledge_points.find((item) => item.id === id)
    if (selected) generation.difficulty = selected.default_difficulty
  },
)

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [contentReport, generationOptions] = await Promise.all([getOwnerContent(), getContentGenerationOptions()])
    records.value = contentReport.records
    breakdowns.value = contentReport.knowledge_points
    options.value = generationOptions
    applyGenerationDefaults()
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '内容数据不可用'
  } finally {
    loading.value = false
  }
}

async function generateDrafts() {
  generating.value = true
  error.value = ''
  lastGenerated.value = []
  try {
    lastGenerated.value = await generateContentDrafts({
      ...generation,
      requirements: generation.requirements.trim(),
    })
    const contentReport = await getOwnerContent()
    records.value = contentReport.records
    breakdowns.value = contentReport.knowledge_points
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : 'AI 题目生成失败'
  } finally {
    generating.value = false
  }
}

async function run(id: string, action: 'validate' | 'review' | 'release' | 'quarantine') {
  acting.value = id
  error.value = ''
  try {
    if (action === 'validate') await validateContent(id)
    if (action === 'review') await reviewContent(id)
    if (action === 'release') await releaseContent(id, reasons.value[id] || 'Owner reviewed all release evidence')
    if (action === 'quarantine') await quarantineContent(id, reasons.value[id] || 'Owner received a content dispute')
    await load()
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '内容操作失败'
  } finally {
    acting.value = ''
  }
}

async function importDraft() {
  error.value = ''
  try {
    await importContentDraft(JSON.parse(importJSON.value))
    importJSON.value = ''
    await load()
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : 'DRAFT JSON 无法导入'
  }
}

onMounted(load)
</script>

<template>
  <main class="page-wrap pb-28 pt-7">
    <header class="flex items-end justify-between gap-4">
      <div>
        <p class="text-sm text-zinc-500">
          发布闸门
        </p>
        <h1 class="mt-1 text-2xl font-semibold">
          内容质量
        </h1>
      </div>
      <button
        class="icon-button"
        type="button"
        aria-label="刷新内容"
        @click="load"
      >
        <RefreshCw :size="19" />
      </button>
    </header>

    <div class="mt-7 grid grid-cols-2 border-y border-zinc-300 py-5">
      <div>
        <strong class="text-3xl tabular-nums">{{ released }}</strong>
        <span class="mt-1 block text-xs text-zinc-500">线上内容</span>
      </div>
      <div>
        <strong class="text-3xl tabular-nums">{{ records.length - released }}</strong>
        <span class="mt-1 block text-xs text-zinc-500">非线上状态</span>
      </div>
    </div>

    <section
      class="border-b border-zinc-300 py-6"
      aria-labelledby="ai-generation-heading"
    >
      <div class="flex items-start gap-3">
        <span
          class="mt-0.5 grid size-9 shrink-0 place-items-center bg-teal-50 text-teal-800"
          aria-hidden="true"
        >
          <Sparkles :size="18" />
        </span>
        <div class="min-w-0">
          <h2
            id="ai-generation-heading"
            class="font-semibold"
          >
            AI 生成题目
          </h2>
          <p class="mt-1 text-xs text-zinc-500">
            {{ options.knowledge_points.length }} 个已发布知识点 · 生成结果仅进入 DRAFT
          </p>
        </div>
      </div>

      <form
        class="mt-5 grid gap-4 sm:grid-cols-2"
        aria-label="AI 生成题目"
        @submit.prevent="generateDrafts"
      >
        <label class="grid min-w-0 gap-1.5 text-sm font-medium">
          学科
          <select
            v-model="selectedSubject"
            name="generation-subject"
            class="h-11 min-w-0 border border-zinc-300 bg-white px-3 text-sm"
          >
            <option
              v-for="subject in subjects"
              :key="subject.code"
              :value="subject.code"
            >{{ subject.name }}</option>
          </select>
        </label>

        <label class="grid min-w-0 gap-1.5 text-sm font-medium">
          学段
          <select
            v-model="selectedGradeBand"
            name="generation-grade-band"
            class="h-11 min-w-0 border border-zinc-300 bg-white px-3 text-sm"
          >
            <option
              v-for="gradeBand in gradeBands"
              :key="gradeBand.code"
              :value="gradeBand.code"
            >{{ gradeBand.name }}</option>
          </select>
        </label>

        <label class="grid min-w-0 gap-1.5 text-sm font-medium">
          课程领域
          <select
            v-model="selectedDomain"
            name="generation-domain"
            class="h-11 min-w-0 border border-zinc-300 bg-white px-3 text-sm"
          >
            <option
              v-for="domain in domains"
              :key="domain.code"
              :value="domain.code"
            >{{ domain.name }} · {{ domain.knowledgePointCount }} 个知识点</option>
          </select>
        </label>

        <label class="grid min-w-0 gap-1.5 text-sm font-medium">
          搜索知识点
          <input
            v-model="knowledgeSearch"
            name="generation-knowledge-search"
            type="search"
            class="h-11 min-w-0 border border-zinc-300 bg-white px-3 text-sm"
            placeholder="输入名称、代码或单元"
          >
        </label>

        <label class="grid min-w-0 gap-1.5 text-sm font-medium sm:col-span-2">
          知识点（{{ filteredKnowledgePoints.length }}）
          <select
            v-model="generation.knowledge_point_id"
            name="generation-knowledge-point"
            class="h-11 min-w-0 border border-zinc-300 bg-white px-3 text-sm"
          >
            <option
              v-for="item in filteredKnowledgePoints"
              :key="item.id"
              :value="item.id"
            >{{ item.unit_name }} · {{ item.name }} [{{ item.knowledge_point_code }}]</option>
          </select>
          <span
            v-if="selectedKnowledgePoint"
            class="min-w-0 text-xs font-normal leading-5 text-zinc-500"
          >{{ selectedKnowledgePoint.domain_name }} / {{ selectedKnowledgePoint.unit_name }} · 来源：{{ selectedKnowledgePoint.curriculum_source_name }}</span>
          <span
            v-else
            class="text-xs font-normal text-amber-800"
          >没有匹配的已发布知识点</span>
        </label>

        <label class="grid min-w-0 gap-1.5 text-sm font-medium">
          难度
          <select
            v-model="generation.difficulty"
            name="generation-difficulty"
            class="h-11 min-w-0 border border-zinc-300 bg-white px-3 text-sm"
          >
            <option
              v-for="level in ['L0', 'L1', 'L2', 'L3', 'L4', 'L5']"
              :key="level"
              :value="level"
            >{{ level }}</option>
          </select>
        </label>

        <label class="grid min-w-0 gap-1.5 text-sm font-medium">
          数量
          <input
            v-model.number="generation.count"
            name="generation-count"
            type="number"
            min="1"
            max="5"
            class="h-11 min-w-0 border border-zinc-300 bg-white px-3 text-sm"
          >
        </label>

        <fieldset class="min-w-0 sm:col-span-2">
          <legend class="text-sm font-medium">
            题型
          </legend>
          <div
            class="mt-1.5 grid grid-cols-2 border border-zinc-300 bg-white p-1"
            aria-label="题型"
          >
            <button
              v-for="type in [{ value: 'FREE_TEXT', label: '自由作答' }, { value: 'MULTIPLE_CHOICE', label: '选择题' }]"
              :key="type.value"
              type="button"
              class="min-h-10 px-3 text-sm font-semibold transition-colors"
              :class="generation.question_type === type.value ? 'bg-zinc-900 text-white' : 'text-zinc-600 hover:text-zinc-950'"
              :aria-pressed="generation.question_type === type.value"
              @click="generation.question_type = type.value as GenerateContentInput['question_type']"
            >
              {{ type.label }}
            </button>
          </div>
        </fieldset>

        <label class="grid min-w-0 gap-1.5 text-sm font-medium sm:col-span-2">
          内容来源
          <select
            v-model="generation.source_id"
            name="generation-source"
            class="h-11 min-w-0 border border-zinc-300 bg-white px-3 text-sm"
          >
            <option
              v-for="source in options.sources"
              :key="source.id"
              :value="source.id"
            >{{ source.name }} · {{ source.license_code }}</option>
          </select>
        </label>

        <label class="grid min-w-0 gap-1.5 text-sm font-medium sm:col-span-2">
          补充要求
          <textarea
            v-model="generation.requirements"
            name="generation-requirements"
            rows="3"
            maxlength="500"
            class="min-w-0 resize-y border border-zinc-300 bg-white p-3 text-sm"
            placeholder="例如：使用校园生活场景，避免复杂小数"
          />
        </label>

        <div class="flex flex-col gap-2 sm:col-span-2 sm:flex-row sm:items-center">
          <button
            type="submit"
            class="primary-button w-full sm:w-auto"
            :disabled="generating || !options.generator_available || !generation.knowledge_point_id || !generation.source_id"
          >
            <LoaderCircle
              v-if="generating"
              class="animate-spin"
              :size="17"
            />
            <Sparkles
              v-else
              :size="17"
            />
            {{ generating ? '正在生成' : `生成 ${generation.count} 道 DRAFT` }}
          </button>
          <span
            v-if="!options.generator_available && !loading"
            class="text-xs text-amber-800"
          >内容生成模型未配置</span>
        </div>
      </form>

      <Transition name="generation-notice">
        <div
          v-if="lastGenerated.length"
          class="mt-6 border-t border-zinc-200 pt-5"
          aria-live="polite"
        >
          <p class="text-sm font-semibold">
            本次已生成 {{ lastGenerated.length }} 道 DRAFT
          </p>
          <ol class="mt-3 divide-y divide-zinc-200 border-y border-zinc-200">
            <li
              v-for="item in lastGenerated"
              :key="item.question_id"
              class="py-3 text-sm leading-6"
            >
              {{ item.prompt }}
            </li>
          </ol>
        </div>
      </Transition>
    </section>

    <details class="border-b border-zinc-300 py-6">
      <summary class="cursor-pointer text-sm font-semibold">
        导入 DRAFT JSON
      </summary>
      <textarea
        v-model="importJSON"
        rows="8"
        class="mt-4 w-full border border-zinc-300 bg-white p-3 font-mono text-xs"
        placeholder="粘贴 { asset, generator } JSON"
      />
      <button
        type="button"
        class="primary-button mt-3"
        :disabled="!importJSON.trim()"
        @click="importDraft"
      >
        <ShieldCheck :size="17" />进入 DRAFT
      </button>
    </details>

    <p
      v-if="error"
      class="mt-6 text-sm text-red-700"
      role="alert"
    >
      {{ error }}
    </p>

    <section
      v-for="point in breakdowns"
      :key="point.knowledge_point_code"
      data-testid="knowledge-point-breakdown"
      class="mt-7 border-y border-zinc-300 py-5"
    >
      <p class="text-xs font-semibold text-zinc-500">
        {{ point.subject }} · {{ point.knowledge_point_code }}
      </p>
      <h2 class="mt-1 font-semibold">
        {{ point.knowledge_point }}
      </h2>
      <dl class="mt-3 space-y-3 text-sm leading-6">
        <div v-if="point.foundation">
          <dt class="text-xs font-semibold text-zinc-500">
            基础
          </dt>
          <dd>{{ point.foundation }}</dd>
        </div>
        <div v-if="point.difficulty_points">
          <dt class="text-xs font-semibold text-zinc-500">
            难度点
          </dt>
          <dd>{{ point.difficulty_points }}</dd>
        </div>
        <div v-if="point.common_stuck_point">
          <dt class="text-xs font-semibold text-zinc-500">
            常见卡点
          </dt>
          <dd>{{ point.common_stuck_point }}</dd>
        </div>
      </dl>
      <div
        v-if="point.release_records.length"
        class="mt-4"
      >
        <h3 class="text-xs font-semibold text-zinc-500">
          发布记录
        </h3>
        <ol class="mt-2 space-y-1 text-xs">
          <li
            v-for="(release, index) in point.release_records"
            :key="`${release.at}-${index}`"
            data-testid="knowledge-point-release"
            class="flex flex-wrap gap-x-3"
          >
            <span class="font-semibold">{{ release.from_status }} → {{ release.to_status }}</span>
            <time
              :datetime="release.at"
              class="text-zinc-500 tabular-nums"
            >{{ new Date(release.at).toLocaleString('zh-CN', { hour12: false }) }}</time>
          </li>
        </ol>
      </div>
    </section>

    <div class="mt-7 divide-y divide-zinc-200">
      <article
        v-for="item in records"
        :key="item.id"
        class="py-5"
      >
        <div class="flex items-start justify-between gap-3">
          <div class="min-w-0">
            <p class="text-xs font-semibold text-zinc-500">
              {{ item.subject }} · {{ item.knowledge_point }}
            </p>
            <h2 class="mt-1 font-semibold leading-6">
              {{ item.prompt }}
            </h2>
          </div>
          <span class="shrink-0 bg-zinc-200 px-2 py-1 text-xs font-semibold">{{ item.status }}</span>
        </div>
        <div class="mt-3 flex flex-wrap gap-4 text-xs">
          <span
            class="inline-flex items-center gap-1"
            :class="item.automatic_validation_passed ? 'text-emerald-700' : 'text-red-700'"
          >
            <component
              :is="item.automatic_validation_passed ? CircleCheck : CircleX"
              :size="15"
            />自动校验
          </span>
          <span
            class="inline-flex items-center gap-1"
            :class="item.secondary_review_passed ? 'text-emerald-700' : 'text-red-700'"
          >
            <component
              :is="item.secondary_review_passed ? CircleCheck : CircleX"
              :size="15"
            />独立复核
          </span>
          <span class="text-zinc-500">{{ item.content_version }}</span>
        </div>
        <div
          v-if="['DRAFT', 'AUTOMATIC_VALIDATED', 'AI_REVIEWED', 'RELEASED'].includes(item.status)"
          class="mt-4 flex flex-wrap items-center gap-2"
        >
          <input
            v-if="item.status === 'AI_REVIEWED' || item.status === 'RELEASED'"
            v-model="reasons[item.id]"
            class="h-10 min-w-0 flex-1 border border-zinc-300 bg-white px-3 text-sm"
            :placeholder="item.status === 'RELEASED' ? '隔离原因' : '发布原因'"
          >
          <button
            v-if="item.status === 'DRAFT'"
            type="button"
            class="secondary-button"
            :disabled="acting === item.id"
            @click="run(item.id, 'validate')"
          >
            <ShieldCheck :size="16" />程序校验
          </button>
          <button
            v-if="item.status === 'AUTOMATIC_VALIDATED'"
            type="button"
            class="secondary-button"
            :disabled="acting === item.id"
            @click="run(item.id, 'review')"
          >
            <ScanSearch :size="16" />独立复核
          </button>
          <button
            v-if="item.status === 'AI_REVIEWED'"
            type="button"
            class="primary-button"
            :disabled="acting === item.id"
            @click="run(item.id, 'release')"
          >
            <Send :size="16" />发布
          </button>
          <button
            v-if="item.status === 'RELEASED'"
            type="button"
            class="secondary-button text-red-700"
            :disabled="acting === item.id"
            @click="run(item.id, 'quarantine')"
          >
            <ShieldX :size="16" />立即隔离
          </button>
        </div>
      </article>
    </div>

    <p
      v-if="loading"
      class="mt-8 text-center text-sm text-zinc-500"
    >
      读取中
    </p>
  </main>
</template>

<style scoped>
.generation-notice-enter-active,
.generation-notice-leave-active {
  transition: opacity 180ms ease, transform 180ms ease;
}

.generation-notice-enter-from,
.generation-notice-leave-to {
  opacity: 0;
  transform: translateY(4px);
}
</style>
