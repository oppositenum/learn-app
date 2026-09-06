<script setup lang="ts">
import { Mic, Square } from '@lucide/vue'
import { onBeforeUnmount, ref } from 'vue'

import { transcribeStudentAudio } from '../api/student'
import { studentInteractionEvent } from '../lib/studentInteraction'

const props = defineProps<{ sessionId: string }>()
const emit = defineEmits<{ transcript: [value: string] }>()
const recording = ref(false)
const busy = ref(false)
const error = ref('')
let recorder: MediaRecorder | undefined
let stream: MediaStream | undefined
let chunks: Blob[] = []
let startedAt = 0

async function toggle() {
  if (recording.value) { recorder?.stop(); return }
  error.value = ''
  try {
    stream = await navigator.mediaDevices.getUserMedia({ audio: true })
    recorder = new MediaRecorder(stream)
    chunks = []
    recorder.addEventListener('dataavailable', (event) => { if (event.data.size > 0) chunks.push(event.data) })
    recorder.addEventListener('stop', handleStop, { once: true })
    startedAt = performance.now()
    recorder.start()
    recording.value = true
    window.dispatchEvent(new Event(studentInteractionEvent))
  } catch { error.value = '无法使用麦克风' }
}

async function handleStop() {
  recording.value = false
  window.dispatchEvent(new Event(studentInteractionEvent))
  stream?.getTracks().forEach((track) => track.stop())
  const duration = Math.max(0.1, (performance.now() - startedAt) / 1000)
  const blob = new Blob(chunks, { type: recorder?.mimeType || 'audio/webm' })
  busy.value = true
  try { emit('transcript', await transcribeStudentAudio(blob, duration, props.sessionId)) } catch (cause) { error.value = cause instanceof Error ? cause.message : '语音识别失败' } finally { busy.value = false }
}

onBeforeUnmount(() => { if (recorder?.state === 'recording') recorder.stop(); stream?.getTracks().forEach((track) => track.stop()) })
</script>

<template>
  <div class="grid min-h-16 w-24 shrink-0 grid-rows-[2.75rem_1.25rem]">
    <button
      type="button"
      class="icon-button border border-zinc-300 bg-white"
      :aria-label="recording ? '停止录音' : '使用语音回答'"
      :disabled="busy"
      @click="toggle"
    >
      <Square
        v-if="recording"
        :size="18"
        class="text-red-600"
      />
      <Mic
        v-else
        :size="20"
      />
    </button>
    <p
      v-if="recording || busy"
      class="mt-1 whitespace-nowrap text-xs font-medium text-zinc-600"
      aria-live="polite"
    >
      {{ recording ? '正在录音' : '识别中' }}
    </p>
    <p
      v-if="error"
      class="mt-1 w-24 text-xs font-medium leading-4 text-red-700"
      role="alert"
    >
      {{ error }}
    </p>
  </div>
</template>
