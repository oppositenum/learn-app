<script setup lang="ts">
import { Save, UserRound } from '@lucide/vue'
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'

import { getParentPreferences, saveParentPreferences, type ParentDomainOption } from '../../api/preferences'
import { getParentChildren } from '../../api/parent'
import { subjectCodes, subjectNames } from '../../lib/subjects'

const route = useRoute()
const studentID = computed(() => typeof route.query.student === 'string' ? route.query.student : '')
const discoveredStudentID = ref('')
const effectiveStudentID = computed(() => studentID.value || discoveredStudentID.value)
const minutes = ref(30)
const priority = ref('MATH')
const reviewOnly = ref(false)
const reduceIntensity = ref(false)
const enabledSubjects = ref<string[]>([...subjectCodes])
const domainOptions = ref<ParentDomainOption[]>([])
const priorityDomain = ref('')
const status = ref('')

// "Today is not a good day" is not a third setting: ticking it records review
// only and reduced intensity together, and it reads as ticked when both are.
const stateNotGood = computed({
  get: () => reviewOnly.value && reduceIntensity.value,
  set: (value: boolean) => {
    reviewOnly.value = value
    reduceIntensity.value = value
  },
})

// Only domains of the subjects the parent has opened can be chosen.
const availableDomains = computed(() => domainOptions.value.filter((option) => enabledSubjects.value.includes(option.subject_code)))
watch(availableDomains, (options) => {
  if (priorityDomain.value && !options.some((option) => option.id === priorityDomain.value)) priorityDomain.value = ''
})

async function save() {
  if (!effectiveStudentID.value) {
    status.value = '尚未绑定孩子账户'
    return
  }
  if (enabledSubjects.value.length === 0) {
    status.value = '至少开放一个学科'
    return
  }
  status.value = '保存中'
  try {
    const result = await saveParentPreferences(effectiveStudentID.value, {
      daily_minutes: minutes.value,
      priority_subject_codes: [priority.value],
      review_only: reviewOnly.value,
      reduce_intensity: reduceIntensity.value,
      enabled_subject_codes: enabledSubjects.value,
      priority_domain_id: priorityDomain.value || null,
      state_not_good: stateNotGood.value,
    })
    // The effective date is the server's; the page never works one out.
    if (result.today_preserved) {
      status.value = `已保存，今日计划保持不变，将从 ${result.applies_from} 生效`
    } else if (result.plan_updated) {
      status.value = `已保存，今日计划已更新，生效日期是 ${result.applies_from}`
    } else {
      status.value = `已保存，生效日期是 ${result.applies_from}`
    }
  } catch (error) {
    status.value = error instanceof Error ? error.message : '保存失败'
  }
}
onMounted(async () => {
  if (!studentID.value) discoveredStudentID.value = (await getParentChildren())[0]?.student_id ?? ''
  if (!effectiveStudentID.value) return
  try {
    const saved = await getParentPreferences(effectiveStudentID.value)
    minutes.value = saved.daily_minutes
    priority.value = saved.priority_subject_codes[0] ?? 'MATH'
    reviewOnly.value = saved.review_only
    reduceIntensity.value = saved.reduce_intensity
    enabledSubjects.value = saved.enabled_subject_codes
    domainOptions.value = saved.domain_options ?? []
    priorityDomain.value = saved.priority_domain_id ?? ''
  } catch { /* 读取失败时保留默认值,保存仍可用 */ }
})
</script>

<template>
  <main class="page-wrap pb-28 pt-7">
    <header class="flex items-start justify-between gap-4">
      <div>
        <p class="text-base text-zinc-500">
          学习计划
        </p>
        <h1 class="mt-1 text-2xl font-semibold">
          家长偏好
        </h1>
      </div>
      <RouterLink
        to="/parent/account"
        class="icon-button parent-icon-action"
        aria-label="账户与退出"
      >
        <UserRound :size="20" />
      </RouterLink>
    </header>
    <form
      class="mt-8 space-y-7"
      @submit.prevent="save"
    >
      <label class="block"><span class="text-base font-semibold">每日总时长</span><input
        v-model.number="minutes"
        type="range"
        min="15"
        max="60"
        step="5"
        class="mt-3 w-full accent-teal-700"
      ><span class="mt-2 block text-base tabular-nums text-zinc-600">{{ minutes }} 分钟</span></label>
      <fieldset class="block">
        <legend class="text-base font-semibold">
          开放学科
        </legend>
        <small class="mt-1 block text-base text-zinc-500">孩子的每日计划只包含勾选的学科,勾几科出几科</small>
        <div class="mt-3 grid grid-cols-2 gap-3">
          <label
            v-for="code in subjectCodes"
            :key="code"
            class="flex min-h-12 items-center gap-3 border border-zinc-300 bg-white px-3"
          ><input
            v-model="enabledSubjects"
            type="checkbox"
            :value="code"
            class="size-5 accent-teal-700"
          ><span class="text-base">{{ subjectNames[code] }}</span></label>
        </div>
      </fieldset>
      <label class="block"><span class="text-base font-semibold">优先学科</span><select
        v-model="priority"
        class="mt-2 min-h-12 w-full border border-zinc-300 bg-white px-3 text-base"
      ><option value="MATH">数学</option><option value="CHINESE">语文</option><option value="ENGLISH">英语</option><option value="PHYSICS">物理</option><option value="CHEMISTRY">化学</option></select></label>
      <label class="block"><span class="text-base font-semibold">明天优先的知识域</span><small class="mt-1 block text-base text-zinc-500">只选领域，不指定题目</small><select
        v-model="priorityDomain"
        data-testid="parent-priority-domain"
        class="mt-2 min-h-12 w-full min-w-0 border border-zinc-300 bg-white px-3 text-base"
      ><option value="">不指定</option><option
        v-for="option in availableDomains"
        :key="option.id"
        :value="option.id"
      >{{ subjectNames[option.subject_code] ?? option.subject_code }} · {{ option.name }}</option></select></label>
      <label class="flex min-h-12 items-center justify-between gap-4 border-y border-zinc-200 py-4"><span><strong class="block text-base">今天状态不好</strong><small class="text-base text-zinc-500">同时只复习、降低强度</small></span><input
        v-model="stateNotGood"
        data-testid="parent-state-not-good"
        type="checkbox"
        class="size-5 shrink-0 accent-teal-700"
      ></label>
      <label class="flex items-center justify-between gap-4 border-b border-zinc-200 pb-4"><span><strong class="block text-base">只复习</strong><small class="text-base text-zinc-500">今天不增加新知识</small></span><input
        v-model="reviewOnly"
        data-testid="parent-review-only"
        type="checkbox"
        class="size-5 shrink-0 accent-teal-700"
      ></label>
      <label class="flex items-center justify-between gap-4 border-b border-zinc-200 pb-4"><span><strong class="block text-base">降低强度</strong><small class="text-base text-zinc-500">减少时长并优先巩固</small></span><input
        v-model="reduceIntensity"
        data-testid="parent-reduce-intensity"
        type="checkbox"
        class="size-5 shrink-0 accent-teal-700"
      ></label>
      <button
        type="submit"
        class="primary-button parent-action w-full"
      >
        <Save :size="18" />保存计划偏好
      </button>
      <p
        data-testid="parent-settings-status"
        class="min-h-6 text-center text-base text-zinc-600"
        aria-live="polite"
      >
        {{ status }}
      </p>
    </form>
  </main>
</template>
