<script setup lang="ts">
import { ArrowLeft, Pause, Play, RotateCcw, Volume2 } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { useLearningStore } from '../../stores/learning'

const learning = useLearningStore()
const route = useRoute()
const router = useRouter()
const player = ref()
const playing = ref(false)
const positionMS = ref(0)
const segments = computed(() => learning.voiceSegments)
const activeIndex = computed(() => segments.value.findIndex((segment) => positionMS.value >= segment.start_ms && positionMS.value < segment.end_ms))
const durationMS = computed(() => segments.value.at(-1)?.end_ms ?? 0)
// The sentence being read, or the last one once playback has ended.
const focusIndex = computed(() => {
	if (activeIndex.value >= 0) return activeIndex.value
	if (durationMS.value && positionMS.value >= durationMS.value) return segments.value.length - 1
	return 0
})
// With audio, only the sentence being read and its neighbours are shown. Without
// audio the whole text stays readable, because it is the explanation.
const shownSegments = computed(() => segments.value
	.map((segment, index) => ({ segment, index }))
	.filter(({ index }) => !learning.voiceAudio || Math.abs(index - focusIndex.value) <= 1))
const classroomRoute = computed(() => route.params.id ? `/student/session/${String(route.params.id)}` : '/student')

async function togglePlayback() { if (learning.status !== 'ACTIVE' || !player.value || !learning.voiceAudio) return; if (player.value.paused) await player.value.play(); else player.value.pause() }
function replaySentence() { const segment = segments.value[Math.max(0, activeIndex.value)]; if (learning.status !== 'ACTIVE' || !player.value || !segment) return; player.value.currentTime = segment.start_ms / 1000; void player.value.play() }
function syncPosition() { if (player.value) positionMS.value = Math.round(player.value.currentTime * 1000) }
function formatTime(milliseconds: number) { const seconds = Math.floor(milliseconds / 1000); return `${String(Math.floor(seconds / 60)).padStart(2, '0')}:${String(seconds % 60).padStart(2, '0')}` }
async function returnToQuestion() {
	if (learning.status !== 'ACTIVE' || !learning.sessionID) return
  await learning.returnFromVoice(learning.sessionID)
  if (!learning.error) await router.push(classroomRoute.value)
}

watch(() => route.params.id, async (value) => {
  const sessionID = String(value || '')
	if (!sessionID) return
	const loaded = learning.sessionID === sessionID || await learning.loadSession(sessionID)
	if (!loaded || String(route.params.id) !== sessionID) return
	if (learning.status === 'PAUSED') return
	if (!learning.error && learning.tutorAction !== 'VOICE_EXPLAIN') await router.replace(`/student/session/${sessionID}`)
}, { immediate: true })
</script>

<template>
  <main class="page-wrap flex h-dvh flex-col pb-6 pt-6">
    <section
      v-if="learning.preparing"
      class="py-8"
      aria-busy="true"
    >
      <div class="h-5 w-36 animate-pulse bg-zinc-200" />
      <div class="mt-5 h-10 w-3/4 animate-pulse bg-zinc-200" />
      <div class="mt-8 h-32 animate-pulse bg-zinc-200" />
      <p class="mt-4 text-base text-zinc-500">
        正在准备语音讲解
      </p>
    </section>
    <template v-else>
      <RouterLink
        :to="classroomRoute"
        class="icon-button classroom-icon-action"
        aria-label="返回课堂"
      >
        <ArrowLeft :size="20" />
      </RouterLink>
      <section class="mt-4 flex min-h-0 flex-1 flex-col">
        <div
          v-if="learning.status === 'PAUSED'"
          data-testid="paused-classroom-notice"
          class="mb-5 border-y border-zinc-300 py-5"
          role="status"
          aria-live="polite"
        >
          <p class="font-medium">
            课堂已暂停
          </p>
          <p class="mt-1 text-base text-zinc-600">
            回到课堂并点击“继续探索”后，才能继续播放讲解。
          </p>
          <RouterLink
            :to="classroomRoute"
            class="primary-button home-action mt-4"
          >
            回到课堂继续探索
            <ArrowLeft
              :size="18"
              aria-hidden="true"
            />
          </RouterLink>
        </div>
        <div class="flex items-center gap-2 text-base font-semibold text-teal-700">
          <Volume2
            :size="18"
            aria-hidden="true"
          />
          AI 老师正在讲解
        </div>
        <h1 class="sr-only">
          听完，再回原题
        </h1>
        <div
          data-testid="voice-sentences"
          class="my-4 flex min-h-0 flex-1 flex-col justify-center gap-3 overflow-y-auto"
          aria-live="polite"
        >
          <p
            v-for="{ segment, index } in shownSegments"
            :key="segment.id"
            :data-voice-sentence="!learning.voiceAudio ? 'text' : index === focusIndex ? 'current' : index < focusIndex ? 'read' : 'unread'"
            :class="!learning.voiceAudio
              ? 'text-lg leading-8 text-zinc-800'
              : index === focusIndex
                ? 'voice-active text-2xl leading-10'
                : index < focusIndex
                  ? 'text-base leading-7 text-zinc-500'
                  : 'text-base leading-7 text-zinc-400'"
          >
            {{ segment.text }}
          </p>
        </div>
        <div class="h-1.5 shrink-0 bg-zinc-200">
          <div
            class="h-full bg-teal-600"
            :style="{ width: `${durationMS ? Math.min(100, positionMS / durationMS * 100) : 0}%` }"
          />
        </div>
        <div class="mt-2 flex shrink-0 justify-between text-base tabular-nums text-zinc-500">
          <span>{{ formatTime(positionMS) }}</span>
          <span>{{ formatTime(durationMS) }}</span>
        </div>

        <audio
          ref="player"
          :src="learning.voiceAudio"
          @timeupdate="syncPosition"
          @play="playing = true"
          @pause="playing = false"
          @ended="playing = false"
        />
        <p
          v-if="!learning.voiceAudio"
          class="mt-4 shrink-0 text-base text-zinc-500"
        >
          语音服务尚未连接，文字讲解仍可使用。
        </p>

        <div class="mt-5 grid shrink-0 grid-cols-2 gap-3">
          <button
            type="button"
            class="primary-button home-action"
            :disabled="learning.status !== 'ACTIVE' || !learning.voiceAudio"
            @click="togglePlayback"
          >
            <component
              :is="playing ? Pause : Play"
              :size="18"
              aria-hidden="true"
            />
            {{ playing ? '暂停' : '播放' }}
          </button>
          <button
            type="button"
            class="secondary-button home-action"
            :disabled="learning.status !== 'ACTIVE' || !learning.voiceAudio"
            @click="replaySentence"
          >
            <RotateCcw
              :size="18"
              aria-hidden="true"
            />
            再听一句
          </button>
        </div>
        <p
          v-if="learning.error"
          data-testid="voice-error"
          class="notice-warm mt-4 shrink-0 px-4 py-3 text-base font-medium"
          role="alert"
        >
          {{ learning.error }}
        </p>
        <button
          type="button"
          class="secondary-button home-action mt-3 w-full shrink-0"
          :disabled="learning.status !== 'ACTIVE' || learning.loading || !learning.sessionID"
          @click="returnToQuestion"
        >
          我懂了，回原题
        </button>
      </section>
    </template>
  </main>
</template>
