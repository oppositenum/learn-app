<script setup lang="ts">
import { ArrowLeft, ArrowRight, RotateCcw } from '@lucide/vue'
import { computed, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { useLearningStore } from '../../stores/learning'

const learning = useLearningStore()
const route = useRoute()
const router = useRouter()
const classroomRoute = computed(() => route.params.id ? `/student/session/${String(route.params.id)}` : '/student')
// A cross-subject backtrack names the subject and knowledge point to fill in
// first. The classroom has already switched to them, so they are the current
// subject and knowledge point.
const backtrack = computed(() => learning.tutorAction === 'BACKTRACK')
const explanation = computed(() => learning.timeline.filter((item) => item.actor === 'TUTOR').at(-1)?.text || '补给内容正在由当前课堂生成。')
async function anotherExample() {
	if (learning.status !== 'ACTIVE' || !learning.sessionID) return
	await learning.requestSupport(learning.sessionID, 'EXPLAIN')
}
async function returnFromBacktrack() {
	const sessionID = String(route.params.id || '')
	if (await learning.returnFromBacktrack(sessionID)) await router.replace(`/student/session/${sessionID}`)
}

watch(() => route.params.id, async (value) => {
  const sessionID = String(value || '')
	if (!sessionID) return
	const loaded = learning.sessionID === sessionID || await learning.loadSession(sessionID)
	if (!loaded || String(route.params.id) !== sessionID) return
	if (learning.status === 'PAUSED') return
	if (!learning.error && learning.tutorAction !== 'EXPLAIN' && learning.tutorAction !== 'BACKTRACK') await router.replace(`/student/session/${sessionID}`)
}, { immediate: true })
</script>

<template>
  <main class="page-wrap min-h-dvh pb-8 pt-6">
    <!-- Outside the loading branch on purpose: the way back to the question
         must exist even while the supply is still being prepared. -->
    <button
      v-if="backtrack"
      type="button"
      class="icon-button classroom-icon-action"
      aria-label="返回原题"
      :disabled="learning.status !== 'ACTIVE' || learning.loading"
      @click="returnFromBacktrack"
    >
      <ArrowLeft :size="20" />
    </button>
    <RouterLink
      v-else
      :to="classroomRoute"
      class="icon-button classroom-icon-action"
      aria-label="返回原题"
    >
      <ArrowLeft :size="20" />
    </RouterLink>
    <section
      v-if="learning.preparing"
      class="py-8"
      aria-busy="true"
    >
      <div class="h-5 w-32 animate-pulse bg-zinc-200" />
      <div class="mt-5 h-10 w-4/5 animate-pulse bg-zinc-200" />
      <div class="mt-8 h-36 animate-pulse bg-zinc-200" />
      <p class="mt-4 text-base text-zinc-500">
        正在准备知识补给
      </p>
    </section>
    <template v-else>
      <section class="mt-8">
        <div
          v-if="learning.status === 'PAUSED'"
          data-testid="paused-classroom-notice"
          class="mb-7 border-y border-zinc-300 py-5"
          role="status"
          aria-live="polite"
        >
          <p class="font-medium">
            课堂已暂停
          </p>
          <p class="mt-1 text-base text-zinc-600">
            回到课堂并点击“继续探索”后，才能继续使用知识补给。
          </p>
          <RouterLink
            :to="classroomRoute"
            class="primary-button home-action mt-4"
          >
            回到课堂继续探索
            <ArrowRight
              :size="18"
              aria-hidden="true"
            />
          </RouterLink>
        </div>
        <p class="text-base font-semibold text-sky-700">
          知识补给站
        </p>
        <template v-if="backtrack">
          <h1
            data-testid="backtrack-reason"
            class="mt-3 text-2xl font-semibold leading-9"
          >
            不是这科不会，是先补一下
          </h1>
          <div class="mt-8 border-y border-zinc-300 py-7">
            <p
              data-testid="backtrack-subject"
              class="text-base text-zinc-500"
            >
              {{ learning.subject }}
            </p>
            <p
              data-testid="backtrack-knowledge-point"
              class="mt-3 text-2xl font-semibold"
            >
              {{ learning.knowledgePoint }}
            </p>
          </div>
        </template>
        <h1
          v-else
          class="mt-3 text-3xl font-semibold leading-10"
        >
          {{ learning.knowledgePoint || '回到当前知识点' }}
        </h1>
        <div
          v-if="!backtrack"
          class="mt-8 border-y border-zinc-300 py-7"
        >
          <p class="text-base text-zinc-500">
            当前讲解
          </p>
          <p class="mt-3 whitespace-pre-line text-2xl font-semibold">
            {{ explanation }}
          </p>
        </div>
        <p
          v-if="learning.error"
          data-testid="supply-error"
          class="notice-warm mt-5 px-4 py-3 text-base font-medium"
          role="alert"
        >
          {{ learning.error }}
        </p>
        <button
          v-if="!backtrack"
          type="button"
          class="secondary-button home-action mt-5 w-full"
          :disabled="learning.status !== 'ACTIVE' || learning.loading || !learning.sessionID"
          @click="anotherExample"
        >
          <RotateCcw
            :size="18"
            aria-hidden="true"
          />
          换个例子
        </button>
        <button
          v-if="backtrack"
          type="button"
          data-testid="backtrack-return"
          class="primary-button home-action mt-8 w-full"
          :disabled="learning.status !== 'ACTIVE' || learning.loading"
          @click="returnFromBacktrack"
        >
          返回原题
          <ArrowRight
            :size="18"
            aria-hidden="true"
          />
        </button>
        <RouterLink
          v-else
          :to="classroomRoute"
          class="primary-button home-action mt-8 w-full"
        >
          返回原题
          <ArrowRight
            :size="18"
            aria-hidden="true"
          />
        </RouterLink>
      </section>
    </template>
  </main>
</template>
