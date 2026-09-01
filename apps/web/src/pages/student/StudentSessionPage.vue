<script setup lang="ts">
import { ArrowLeft, CalendarCheck2, Lightbulb, Pause, Send, StopCircle, Volume2 } from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { useLearningStore } from '../../stores/learning'
import VoiceCaptureButton from '../../components/VoiceCaptureButton.vue'

const learning = useLearningStore()
const answer = ref('')
const route = useRoute()
const router = useRouter()
const clock = ref(Date.now())
const timer = window.setInterval(() => { clock.value = Date.now() }, 1000)
const elapsed = computed(() => {
  const seconds = learning.startedAt ? Math.max(0, Math.floor((clock.value - new Date(learning.startedAt).getTime()) / 1000)) : 0
  return `${String(Math.floor(seconds / 60)).padStart(2, '0')}:${String(seconds % 60).padStart(2, '0')}`
})

async function submit() {
  await learning.submitAnswer(String(route.params.id), answer.value)
  answer.value = ''
  if (learning.tutorAction === 'VOICE_EXPLAIN') await router.push(`/student/session/${String(route.params.id)}/voice`)
}

async function support(type: 'HINT' | 'EXPLAIN') {
  await learning.requestSupport(String(route.params.id), type)
  if (type === 'EXPLAIN' && !learning.error) await router.push(`/student/session/${String(route.params.id)}/supply`)
}

watch(() => learning.sessionGone, (gone) => { if (gone) router.replace('/student') })

onMounted(() => learning.loadSession(String(route.params.id)))
onBeforeUnmount(() => window.clearInterval(timer))
</script>

<template>
  <main class="mx-auto min-h-svh w-full max-w-3xl pb-28">
    <header class="sticky top-0 z-10 border-b border-zinc-200 bg-[#f5f5f1]/95 px-4 py-3 backdrop-blur sm:px-7">
      <div class="flex items-center justify-between gap-3">
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
            {{ learning.difficulty }}
          </p>
        </div>
        <span class="text-sm font-medium tabular-nums text-zinc-500">{{ elapsed }}</span>
      </div>
      <div
        class="mt-3 grid grid-cols-4 gap-1"
        :aria-label="`本节启发第 ${learning.socraticRound} 轮`"
      >
        <span
          v-for="step in 4"
          :key="step"
          class="h-1"
          :class="step <= Math.max(1, learning.socraticRound) ? 'bg-teal-600' : 'bg-zinc-200'"
        />
      </div>
    </header>

    <section
      class="px-4 py-7 sm:px-7"
      aria-label="当前课堂"
    >
      <div class="flex items-center gap-2 text-sm font-semibold text-teal-700">
        <span class="grid size-7 place-items-center bg-teal-100">AI</span>
        <span>{{ learning.tutorAction === 'ASK' ? '先说说你的思路' : learning.tutorAction === 'BREAK' ? '先停一下，不继续追问' : learning.tutorAction === 'RETURN' ? '回到原问题验证' : '换一个角度继续想' }}</span>
      </div>
      <h1 class="mt-5 text-[1.65rem] font-semibold leading-10 sm:text-3xl">
        {{ learning.prompt }}
      </h1>

      <div
        v-if="learning.timeline.length > 1"
        class="mt-6 border-l-2 border-teal-500 pl-4"
      >
        <p class="text-sm font-semibold text-teal-800">
          AI 正在调整方法
        </p>
        <p class="mt-1 whitespace-pre-line leading-7 text-zinc-700">
          {{ learning.timeline.at(-1)?.text }}
        </p>
      </div>

      <form
        class="mt-8"
        @submit.prevent="submit"
      >
        <p
          v-if="learning.error"
          class="mb-3 text-sm font-medium text-red-700"
          role="alert"
        >
          {{ learning.error }}
        </p>
        <label
          for="student-answer"
          class="text-sm font-semibold"
        >把你的想法写下来</label>
        <textarea
          id="student-answer"
          v-model="answer"
          rows="4"
          class="mt-2 w-full resize-none border border-zinc-300 bg-white p-4 text-base leading-7 outline-none transition-colors focus:border-teal-600 focus:ring-2 focus:ring-teal-100"
          placeholder="不必只写答案，也可以说说你准备先算什么"
        />
        <div class="mt-3 flex items-center justify-between gap-3">
          <VoiceCaptureButton
            :session-id="String(route.params.id)"
            @transcript="answer = $event"
          />
          <button
            type="submit"
            class="primary-button flex-1"
            :disabled="!answer.trim() || learning.loading"
          >
            <Send
              :size="18"
              aria-hidden="true"
            />
            {{ learning.loading ? '分析中' : '提交想法' }}
          </button>
        </div>
      </form>

      <div class="mt-5 grid grid-cols-2 gap-3">
        <button
          type="button"
          class="secondary-button"
          :disabled="learning.loading"
          @click="support('HINT')"
        >
          <Lightbulb
            :size="18"
            aria-hidden="true"
          />
          一点提示
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
          />
          我不会
        </button>
      </div>

      <section
        v-if="learning.tutorAction === 'COMPLETE'"
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
  </main>
</template>
