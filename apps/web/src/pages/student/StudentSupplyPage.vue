<script setup lang="ts">
import { ArrowLeft, ArrowRight, RotateCcw } from '@lucide/vue'
import { computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { useLearningStore } from '../../stores/learning'

const learning = useLearningStore()
const route = useRoute()
const router = useRouter()
const classroomRoute = computed(() => learning.sessionID ? `/student/session/${learning.sessionID}` : '/student')
const explanation = computed(() => learning.timeline.filter((item) => item.actor === 'TUTOR').at(-1)?.text || '补给内容正在由当前课堂生成。')
async function anotherExample() { if (learning.sessionID) await learning.requestSupport(learning.sessionID, 'EXPLAIN') }

onMounted(async () => {
  const sessionID = String(route.params.id || '')
  if (sessionID && learning.sessionID !== sessionID) await learning.loadSession(sessionID)
  if (!learning.error && learning.tutorAction !== 'EXPLAIN') await router.replace(classroomRoute.value)
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
      <p class="text-sm font-semibold text-sky-700">
        知识补给站
      </p>
      <h1 class="mt-3 text-3xl font-semibold leading-10">
        {{ learning.knowledgePoint || '回到当前知识点' }}
      </h1>
      <div class="mt-8 border-y border-zinc-300 py-7">
        <p class="text-sm text-zinc-500">
          当前讲解
        </p>
        <p class="mt-3 whitespace-pre-line text-2xl font-semibold">
          {{ explanation }}
        </p>
      </div>
      <button
        type="button"
        class="secondary-button mt-5 w-full"
        :disabled="learning.loading || !learning.sessionID"
        @click="anotherExample"
      >
        <RotateCcw
          :size="18"
          aria-hidden="true"
        />
        换个例子
      </button>
      <RouterLink
        :to="classroomRoute"
        class="primary-button mt-8 w-full"
      >
        返回原题
        <ArrowRight
          :size="18"
          aria-hidden="true"
        />
      </RouterLink>
    </section>
  </main>
</template>
