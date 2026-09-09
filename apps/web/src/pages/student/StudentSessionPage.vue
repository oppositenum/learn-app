<script setup lang="ts">
import { ArrowLeft, CalendarCheck2, Lightbulb, Pause, RefreshCw, Send, ShieldAlert, StopCircle, Volume2 } from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import VoiceCaptureButton from '../../components/VoiceCaptureButton.vue'
import StudentInteractionRenderer from '../../components/StudentInteractionRenderer.vue'
import { difficultyLabels, tutorActionLabels } from '../../lib/learningLabels'
import { classroomStageLabels, classroomStages, isStructuredInteraction, stageIdentity, stageTaskKey, structuredResponseComplete } from '../../lib/studentInteraction'
import { useLearningStore } from '../../stores/learning'

const learning = useLearningStore()
const route = useRoute()
const router = useRouter()
const answer = computed({
	get: () => learning.answerDraftFor(String(route.params.id || '')),
	set: (value: string) => learning.setAnswerDraft(String(route.params.id || ''), value),
})
const monotonicNow = ref(performance.now())
let timer = 0

const runningDelta = computed(() => learning.status === 'ACTIVE' && learning.timingClientAt > 0
  ? Math.max(0, Math.floor((monotonicNow.value - learning.timingClientAt) / 1000))
  : 0)
const totalSeconds = computed(() => learning.activeSeconds + runningDelta.value)
const currentSeconds = computed(() => learning.currentActiveSeconds + runningDelta.value)
const currentTutorTurn = computed(() => learning.timeline.filter((item) => item.actor === 'TUTOR').at(-1))
const showTutorTurn = computed(() => learning.tutorAction !== 'ASK' && currentTutorTurn.value?.meta !== 'ASK')
const complete = computed(() => learning.tutorAction === 'COMPLETE' || learning.status === 'COMPLETED')
const structured = computed(() => isStructuredInteraction(learning.interaction ?? undefined, learning.stageFlow ?? undefined))
const activeStageIdentity = computed(() => stageIdentity(learning.sessionID, learning.questionID, learning.stageFlow ?? undefined))
const structuredDraft = computed({
  get: () => learning.structuredDraft,
  set: (value: Record<string, unknown>) => {
    if (!activeStageIdentity.value) return
    learning.setStructuredDraft(stageTaskKey(learning.sessionID, activeStageIdentity.value), value)
  },
})
const structuredComplete = computed(() => Boolean(structured.value && learning.interaction && structuredResponseComplete(learning.interaction, structuredDraft.value)))
const activeStageIndex = computed(() => classroomStages.indexOf(learning.stageFlow?.stage as (typeof classroomStages)[number]))
const stageMaterialUnavailable = computed(() => Boolean(learning.stageFlow && learning.interaction?.fallback && !complete.value))

function formatDuration(seconds: number) {
  const safe = Math.max(0, Math.floor(seconds))
  const hours = Math.floor(safe / 3600)
  const minutes = Math.floor((safe % 3600) / 60)
  const remainder = safe % 60
  return hours > 0
    ? `${hours}:${String(minutes).padStart(2, '0')}:${String(remainder).padStart(2, '0')}`
    : `${String(minutes).padStart(2, '0')}:${String(remainder).padStart(2, '0')}`
}

async function openSession(sessionID: string) {
	await learning.loadSession(sessionID)
}

async function submit() {
	if (learning.status !== 'ACTIVE') return
	if (structured.value) {
		if (!structuredComplete.value) return
		await learning.submitStageResponse(String(route.params.id), structuredDraft.value)
		return
	}
  const accepted = await learning.submitAnswer(String(route.params.id), answer.value)
  if (!accepted) return
  answer.value = ''
  if (learning.tutorAction === 'VOICE_EXPLAIN') await router.push(`/student/session/${String(route.params.id)}/voice`)
}

async function support(type: 'HINT' | 'EXPLAIN') {
	if (learning.status !== 'ACTIVE') return
	const accepted = structured.value
		? await learning.requestStageSupport(String(route.params.id), type)
		: await learning.requestSupport(String(route.params.id), type)
  if (accepted && type === 'EXPLAIN') await router.push(`/student/session/${String(route.params.id)}/supply`)
}

async function abandon() {
	if (!window.confirm('结束后，这次探索不会计入完成记录。确定结束吗？')) return
	if (await learning.abandonSession()) await router.replace('/student')
}

watch(() => route.params.id, (sessionID) => {
  if (sessionID) void openSession(String(sessionID))
}, { immediate: true })
watch(() => learning.sessionGone, (gone) => { if (gone) void router.replace('/student') })

onMounted(() => {
  timer = window.setInterval(() => { monotonicNow.value = performance.now() }, 1000)
})
onBeforeUnmount(() => window.clearInterval(timer))
</script>

<template>
  <main class="mx-auto min-h-dvh w-full max-w-3xl pb-8">
    <section
      v-if="learning.preparing"
      class="px-4 py-8 sm:px-7"
      aria-busy="true"
      aria-live="polite"
    >
      <div class="h-3 w-28 animate-pulse bg-zinc-200" />
      <div class="mt-8 h-8 w-4/5 animate-pulse bg-zinc-200" />
      <div class="mt-3 h-8 w-2/3 animate-pulse bg-zinc-200" />
      <div class="mt-10 h-36 animate-pulse bg-white" />
      <p class="mt-5 text-sm font-medium text-zinc-600">
        正在准备这次探索
      </p>
    </section>

    <section
      v-else-if="!learning.sessionID"
      class="px-4 py-10 sm:px-7"
    >
      <h1 class="text-xl font-semibold">
        课堂暂时没有准备好
      </h1>
      <p
        v-if="learning.error"
        class="mt-3 text-sm text-red-700"
        role="alert"
      >
        {{ learning.error }}
      </p>
      <button
        type="button"
        class="secondary-button mt-5"
        @click="openSession(String(route.params.id))"
      >
        <RefreshCw :size="17" />重新加载
      </button>
    </section>

    <template v-else>
      <header class="sticky top-0 z-10 border-b border-zinc-200 bg-[#f5f5f1]/95 px-4 py-3 backdrop-blur sm:px-7">
        <div class="grid grid-cols-[2.75rem_minmax(0,1fr)_2.75rem] items-center gap-2">
          <RouterLink
            to="/student"
            class="icon-button"
            aria-label="返回首页"
          >
            <ArrowLeft :size="20" />
          </RouterLink>
          <div class="min-w-0 text-center">
            <p class="truncate text-sm font-semibold">
              {{ learning.subject }} · {{ learning.knowledgePoint }}
            </p>
            <p class="text-xs text-zinc-500">
              {{ difficultyLabels[learning.difficulty] ?? '适合当前进度' }}
            </p>
          </div>
          <button
            v-if="!complete && learning.status !== 'ABANDONED'"
            type="button"
            class="icon-button"
            :disabled="learning.loading"
            aria-label="结束本次探索"
            title="结束本次探索"
            @click="abandon"
          >
            <StopCircle :size="19" />
          </button>
          <span
            v-else
            class="text-right text-xs font-medium text-zinc-500"
          >{{ learning.status === 'PAUSED' ? '已暂停' : '' }}</span>
        </div>
        <div class="mt-3 flex items-center justify-center gap-5 text-xs text-zinc-500">
          <span data-testid="segment-timer">本段用时 <strong class="inline-block min-w-12 font-semibold tabular-nums text-zinc-700">{{ formatDuration(currentSeconds) }}</strong></span>
          <span data-testid="session-timer">本节累计 <strong class="inline-block min-w-12 font-semibold tabular-nums text-zinc-700">{{ formatDuration(totalSeconds) }}</strong></span>
        </div>
        <div
          v-if="learning.socraticRound > 0"
          class="mt-3 grid grid-cols-3 gap-1"
          :aria-label="`已经换了 ${learning.socraticRound} 种启发方式`"
        >
          <span
            v-for="step in 3"
            :key="step"
            class="h-1"
            :class="step <= learning.socraticRound ? 'bg-teal-600' : 'bg-zinc-200'"
          />
        </div>
        <ol
          v-if="learning.stageFlow && !complete"
          class="mt-3 grid grid-cols-4 gap-1"
          aria-label="课堂阶段"
        >
          <li
            v-for="(stage, index) in classroomStages"
            :key="stage"
            class="min-w-0 border-t-2 pt-1 text-center text-[0.7rem] font-semibold"
            :class="index <= activeStageIndex ? 'border-teal-600 text-teal-800' : 'border-zinc-300 text-zinc-500'"
            :aria-current="stage === learning.stageFlow.stage ? 'step' : undefined"
          >
            {{ classroomStageLabels[stage] }}
          </li>
        </ol>
      </header>

      <section
        class="px-4 py-6 sm:px-7"
        aria-label="当前课堂"
      >
        <div class="flex items-center gap-2 text-sm font-semibold text-teal-700">
          <span class="grid size-7 place-items-center bg-teal-100">AI</span>
          <span>{{ tutorActionLabels[learning.tutorAction] }}</span>
        </div>
        <h1 class="mt-5 text-[1.65rem] font-semibold leading-10 sm:text-3xl">
          {{ learning.prompt }}
        </h1>

        <div
          v-if="showTutorTurn"
          data-tutor-turn
          class="mt-6 border-l-2 border-teal-500 pl-4"
        >
          <p class="whitespace-pre-line leading-7 text-zinc-700">
            {{ currentTutorTurn?.text }}
          </p>
        </div>

        <div
          v-if="learning.safetyNotice"
          data-testid="safety-notice"
          class="mt-6 border-y border-amber-300 bg-amber-50 px-4 py-4 text-amber-950"
          role="alert"
          aria-live="assertive"
        >
          <div class="flex items-start gap-3">
            <ShieldAlert
              :size="20"
              class="mt-0.5 shrink-0 text-amber-700"
              aria-hidden="true"
            />
            <div class="min-w-0">
              <p class="font-semibold">
                安全提醒
              </p>
              <p class="mt-1 whitespace-pre-line text-sm leading-6">
                {{ learning.safetyNotice.message }}
              </p>
              <p
                v-if="learning.safetyNotice.parent_notified"
                class="mt-2 text-xs font-medium text-amber-800"
              >
                已按安全规则通知家长，通知中不包含你刚才输入的原话。
              </p>
            </div>
          </div>
        </div>

        <p
          v-if="learning.error"
          class="mt-5 text-sm font-medium text-red-700"
          role="alert"
        >
          {{ learning.error }}
        </p>

        <div
          v-if="learning.status === 'ABANDONED'"
          class="mt-7 border-y border-zinc-200 py-5"
        >
          <p class="font-medium">
            这次探索已经结束
          </p>
          <RouterLink
            to="/student"
            class="primary-button mt-3"
          >
            返回今日计划
          </RouterLink>
        </div>

        <div
          v-if="stageMaterialUnavailable"
          class="mt-7 border-y border-amber-300 bg-amber-50 py-5 text-amber-950"
          role="alert"
        >
          <p class="font-semibold">
            当前任务暂时只能显示文字题面
          </p>
          <p class="mt-1 text-sm leading-6">
            校验材料没有完整载入，本次不会记录答题证据。
          </p>
          <button
            type="button"
            class="secondary-button mt-3"
            :disabled="learning.preparing"
            @click="openSession(String(route.params.id))"
          >
            <RefreshCw :size="17" />重新加载任务
          </button>
        </div>

        <div
          v-if="!complete && learning.status !== 'ABANDONED' && !stageMaterialUnavailable"
          data-testid="classroom-composer"
          data-layout-contract="normal-flow"
          class="classroom-composer mt-8 border-t border-zinc-200 bg-[#f5f5f1] py-4 pb-[max(1rem,env(safe-area-inset-bottom))]"
        >
          <div
            v-if="learning.status === 'PAUSED'"
            class="mb-5 border-b border-zinc-200 pb-5"
            aria-live="polite"
          >
            <p class="font-medium">
              这次探索已暂停
            </p>
            <p class="mt-1 text-sm text-zinc-600">
              继续后可以接着回答
            </p>
            <button
              type="button"
              data-testid="resume-session"
              class="primary-button mt-3"
              :disabled="learning.loading"
              @click="learning.resumeSession()"
            >
              <RefreshCw :size="17" />{{ learning.loading ? '正在继续' : '继续探索' }}
            </button>
          </div>

          <form
            @submit.prevent="submit"
          >
            <fieldset
              data-testid="answer-controls"
              class="m-0 border-0 p-0 transition-opacity"
              :class="learning.status === 'PAUSED' ? 'opacity-55' : ''"
              :disabled="learning.status !== 'ACTIVE' || learning.loading"
              :aria-disabled="learning.status !== 'ACTIVE' || learning.loading"
            >
              <label
                v-if="!structured"
                for="student-answer"
                class="text-sm font-semibold"
              >把你的想法写下来</label>
              <textarea
                v-if="!structured"
                id="student-answer"
                v-model="answer"
                rows="3"
                class="mt-2 w-full resize-none border border-zinc-300 bg-white p-4 text-base leading-7 outline-none transition-colors focus:border-teal-600 focus:ring-2 focus:ring-teal-100"
                placeholder="不必只写答案，也可以说说你准备先算什么"
              />
              <StudentInteractionRenderer
                v-else-if="learning.interaction"
                v-model="structuredDraft"
                :interaction="learning.interaction"
                :disabled="learning.loading"
              />
              <div class="mt-3 flex items-center justify-between gap-3">
                <VoiceCaptureButton
                  v-if="!structured"
                  :session-id="String(route.params.id)"
                  @transcript="answer = $event"
                />
                <button
                  type="submit"
                  class="primary-button min-w-0 flex-1"
                  :disabled="structured ? !structuredComplete || learning.loading : !answer.trim() || learning.loading"
                >
                  <Send
                    :size="18"
                    aria-hidden="true"
                  />
                  {{ learning.loading ? '检查中' : structured ? '提交答案' : '提交想法' }}
                </button>
              </div>
              <p
                v-if="structured ? !structuredComplete : !answer.trim()"
                class="mt-2 text-xs text-zinc-500"
              >
                {{ structured ? '请完成当前任务' : '先写下你的想法' }}
              </p>
              <div class="mt-4 grid grid-cols-2 gap-3">
                <button
                  type="button"
                  class="secondary-button"
                  :disabled="learning.loading"
                  @click="support('HINT')"
                >
                  <Lightbulb
                    :size="18"
                    aria-hidden="true"
                  />一点提示
                </button>
                <button
                  type="button"
                  class="secondary-button"
                  :disabled="learning.loading"
                  @click="support('EXPLAIN')"
                >
                  <Volume2
                    :size="18"
                    aria-hidden="true"
                  />我不会
                </button>
              </div>
            </fieldset>
          </form>
        </div>

        <section
          v-if="complete"
          class="mt-8 border-t border-zinc-300 pt-6"
          aria-labelledby="reflection-title"
        >
          <h2
            id="reflection-title"
            class="text-lg font-semibold"
          >
            明天还想继续吗？
          </h2>
          <div class="mt-4 grid gap-3 sm:grid-cols-3">
            <button
              type="button"
              class="primary-button"
              @click="learning.reflect(String(route.params.id), 'CONTINUE_TOMORROW')"
            >
              <CalendarCheck2 :size="18" />明天继续
            </button>
            <button
              type="button"
              class="secondary-button"
              @click="learning.reflect(String(route.params.id), 'PAUSE')"
            >
              <Pause :size="18" />先停一停
            </button>
            <button
              type="button"
              class="secondary-button"
              @click="learning.reflect(String(route.params.id), 'STOP')"
            >
              <StopCircle :size="18" />不再继续
            </button>
          </div>
          <p
            class="mt-3 min-h-5 text-sm text-zinc-600"
            aria-live="polite"
          >
            {{ learning.reflectionStatus }}
          </p>
        </section>
      </section>
    </template>
  </main>
</template>
