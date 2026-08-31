<script setup lang="ts">
import { ArrowLeft, Pause, Play, RotateCcw, Volume2 } from '@lucide/vue'
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { useLearningStore } from '../../stores/learning'

const learning = useLearningStore()
const route = useRoute()
const router = useRouter()
const player = ref()
const playing = ref(false)
const positionMS = ref(0)
const segments = computed(() => learning.voiceSegments.length > 0 ? learning.voiceSegments : [
  { id: 'pending-1', text: '先看一个不同数字的平行例子。', start_ms: 0, end_ms: 2500 },
  { id: 'pending-2', text: '把固定部分和随数量变化的部分分开。', start_ms: 2500, end_ms: 5500 },
  { id: 'pending-3', text: '再回到原题，自己试一次。', start_ms: 5500, end_ms: 8000 },
])
const activeIndex = computed(() => segments.value.findIndex((segment) => positionMS.value >= segment.start_ms && positionMS.value < segment.end_ms))
const durationMS = computed(() => segments.value.at(-1)?.end_ms ?? 0)
const classroomRoute = computed(() => learning.sessionID ? `/student/session/${learning.sessionID}` : '/student')

async function togglePlayback() { if (!player.value || !learning.voiceAudio) return; if (player.value.paused) await player.value.play(); else player.value.pause() }
function replaySentence() { const segment = segments.value[Math.max(0, activeIndex.value)]; if (!player.value || !segment) return; player.value.currentTime = segment.start_ms / 1000; void player.value.play() }
function syncPosition() { if (player.value) positionMS.value = Math.round(player.value.currentTime * 1000) }
function formatTime(milliseconds: number) { const seconds = Math.floor(milliseconds / 1000); return `${String(Math.floor(seconds / 60)).padStart(2, '0')}:${String(seconds % 60).padStart(2, '0')}` }
async function returnToQuestion() {
  if (!learning.sessionID) return
  await learning.returnFromVoice(learning.sessionID)
  if (!learning.error) await router.push(classroomRoute.value)
}

onMounted(async () => {
  const sessionID = String(route.params.id || '')
  if (sessionID && learning.sessionID !== sessionID) await learning.loadSession(sessionID)
  if (!learning.error && learning.tutorAction !== 'VOICE_EXPLAIN') await router.replace(classroomRoute.value)
})
</script>

<template>
  <main class="page-wrap min-h-svh pb-28 pt-6">
    <RouterLink
      :to="classroomRoute"
      class="icon-button"
      aria-label="返回课堂"
    >
      <ArrowLeft :size="20" />
    </RouterLink>
    <section class="mt-8">
      <div class="flex items-center gap-2 text-sm font-semibold text-teal-700">
        <Volume2
          :size="18"
          aria-hidden="true"
        />
        AI 老师正在讲解
      </div>
      <h1 class="mt-5 text-3xl font-semibold">
        听完，再回原题
      </h1>
      <div class="mt-8 h-1.5 bg-zinc-200">
        <div
          class="h-full bg-teal-600"
          :style="{ width: `${durationMS ? Math.min(100, positionMS / durationMS * 100) : 0}%` }"
        />
      </div>
      <div class="mt-2 flex justify-between text-sm tabular-nums text-zinc-500">
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
      <div
        class="mt-10 space-y-5 text-xl leading-9 text-zinc-400"
        aria-live="polite"
      >
        <p
          v-for="(segment, index) in segments"
          :key="segment.id"
          :class="{ 'voice-active': activeIndex === index }"
        >
          {{ segment.text }}
        </p>
      </div>
      <p
        v-if="!learning.voiceAudio"
        class="mt-6 text-sm text-zinc-500"
      >
        语音服务尚未连接，文字讲解仍可使用。
      </p>

      <div class="mt-10 grid grid-cols-2 gap-3">
        <button
          type="button"
          class="primary-button"
          :disabled="!learning.voiceAudio"
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
          class="secondary-button"
          :disabled="!learning.voiceAudio"
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
        class="mt-4 text-sm font-medium text-red-700"
        role="alert"
      >
        {{ learning.error }}
      </p>
      <button
        type="button"
        class="secondary-button mt-3 w-full"
        :disabled="learning.loading || !learning.sessionID"
        @click="returnToQuestion"
      >
        我懂了，回原题
      </button>
    </section>
  </main>
</template>
