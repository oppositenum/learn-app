<script setup lang="ts">
import { Home, Sparkles, UserRound } from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { studentInteractionEvent, studentInteractionFreshnessMS } from '../lib/studentInteraction'
import { roleHome, useAuthSession } from '../stores/auth'
import { useLearningStore } from '../stores/learning'

const route = useRoute()
const router = useRouter()
const auth = useAuthSession()
const learning = useLearningStore()
const showNavigation = computed(() => !route.meta.hideStudentNav)
const heartbeatIntervalMS = 30_000
let stopNavigationGuard: (() => void) | undefined
let heartbeatTimer = 0
let backgroundPausePending = false
let backgroundPauseOperation: Promise<boolean> | null = null
let hiddenReconciliation = Promise.resolve()
let visibleReconciliation: Promise<void> | null = null
let interactionSessionID = ''
let lastInteractionAt = Number.NEGATIVE_INFINITY

function sameClassroomScope(target: typeof route, source: typeof route) {
  return Boolean(target.meta.classroom && source.meta.classroom && String(target.params.id) === String(source.params.id))
}

function isDocumentVisible() {
	return document.visibilityState === 'visible'
}

function recordStudentInteraction() {
	if (!route.meta.classroom || !learning.sessionID) return
	interactionSessionID = learning.sessionID
	lastInteractionAt = Date.now()
}

function hasFreshStudentInteraction() {
	return interactionSessionID === learning.sessionID
		&& Date.now() - lastInteractionAt <= studentInteractionFreshnessMS
}

function pauseForBackground() {
  if (!backgroundPauseOperation) {
    backgroundPauseOperation = learning.pauseForVisibility(true).finally(() => {
      backgroundPauseOperation = null
    })
  }
  return backgroundPauseOperation
}

async function syncStudentIdentity() {
	const previousUserID = auth.user.value?.user_id
	let current
	try {
		current = await auth.refresh()
	} catch {
		return false
	}
	if (!current) {
		await router.replace('/login')
		return false
	}
	if (current.role !== 'STUDENT' || (previousUserID && current.user_id !== previousUserID)) {
		await router.replace(roleHome(current.role))
		return false
	}
	return true
}

async function reconcileHidden() {
	if (!route.meta.classroom || learning.status !== 'ACTIVE' || !learning.sessionID) return
	backgroundPausePending = true
	if (learning.submissionInFlight) return
	const paused = await pauseForBackground()
	backgroundPausePending = !paused
}

async function reconcileVisible() {
	if (!isDocumentVisible()) return
	if (backgroundPauseOperation) await backgroundPauseOperation.catch(() => false)
	if (!isDocumentVisible()) return
	if (!await syncStudentIdentity()) return
	if (!isDocumentVisible()) return
	if (route.meta.classroom && learning.sessionID) {
		await learning.waitForPendingResume()
		if (!isDocumentVisible()) return
		if (!await learning.refreshSession()) return
	}
	if (!isDocumentVisible()) return
	backgroundPausePending = false
}

function queueVisibleReconciliation() {
	if (!visibleReconciliation) {
		visibleReconciliation = Promise.resolve().then(reconcileVisible).finally(() => {
			visibleReconciliation = null
		})
	}
	return visibleReconciliation
}

function queueBackgroundReconciliation() {
	hiddenReconciliation = hiddenReconciliation.then(() => reconcileHidden(), () => reconcileHidden())
	return hiddenReconciliation
}

function handleVisibility() {
	if (isDocumentVisible()) {
		void queueVisibleReconciliation()
		return
	}
	void queueBackgroundReconciliation()
}

function handlePageHide() {
	if (!route.meta.classroom || learning.status !== 'ACTIVE' || !learning.sessionID) return
	backgroundPausePending = true
	void queueBackgroundReconciliation()
}

function handlePageShow(event: PageTransitionEvent) {
	if (!event.persisted || !isDocumentVisible()) return
	void queueVisibleReconciliation()
}

const stopLearningWatch = watch(
	() => [learning.sessionID, learning.status, learning.loading, learning.preparing, learning.submissionInFlight],
	() => {
		if (!isDocumentVisible()) {
			void queueBackgroundReconciliation()
			return
		}
		if (backgroundPausePending) void queueVisibleReconciliation()
	},
)

onMounted(() => {
  document.addEventListener('visibilitychange', handleVisibility)
  window.addEventListener('pagehide', handlePageHide)
  window.addEventListener('pageshow', handlePageShow)
  window.addEventListener('keydown', recordStudentInteraction)
  window.addEventListener('pointerdown', recordStudentInteraction, { passive: true })
  document.addEventListener('scroll', recordStudentInteraction, { capture: true, passive: true })
  window.addEventListener(studentInteractionEvent, recordStudentInteraction)
  heartbeatTimer = window.setInterval(() => {
		if (!route.meta.classroom || learning.status !== 'ACTIVE' || !learning.sessionID) return
		if (isDocumentVisible() && hasFreshStudentInteraction()) {
			void learning.heartbeat()
			return
		}
		if (isDocumentVisible() && learning.submissionInFlight) {
			void learning.heartbeat()
			return
		}
		if (!hasFreshStudentInteraction()) void learning.refreshSession()
  }, heartbeatIntervalMS)
	stopNavigationGuard = router.beforeEach(async (to, from) => {
		if (!from.meta.classroom || sameClassroomScope(to as typeof route, from as typeof route)) return true
		await learning.waitForPendingResume()
		if (learning.status !== 'ACTIVE') return true
		if (!window.confirm('要先暂停这次探索再离开吗？')) return false
		return learning.pauseSession()
  })
})

onBeforeUnmount(() => {
  document.removeEventListener('visibilitychange', handleVisibility)
  window.removeEventListener('pagehide', handlePageHide)
  window.removeEventListener('pageshow', handlePageShow)
	window.removeEventListener('keydown', recordStudentInteraction)
	window.removeEventListener('pointerdown', recordStudentInteraction)
	document.removeEventListener('scroll', recordStudentInteraction, true)
	window.removeEventListener(studentInteractionEvent, recordStudentInteraction)
	window.clearInterval(heartbeatTimer)
	stopNavigationGuard?.()
	stopLearningWatch()
})
</script>

<template>
  <div class="student-shell min-h-svh bg-[#f5f5f1] text-zinc-900">
    <RouterView />
    <nav
      v-if="showNavigation"
      class="bottom-nav"
      aria-label="学生导航"
    >
      <RouterLink
        to="/student"
        class="nav-item"
      >
        <Home
          :size="20"
          aria-hidden="true"
        />
        <span>首页</span>
      </RouterLink>
      <RouterLink
        to="/student/growth"
        class="nav-item"
      >
        <Sparkles
          :size="20"
          aria-hidden="true"
        />
        <span>成长</span>
      </RouterLink>
      <RouterLink
        to="/student/profile"
        class="nav-item"
      >
        <UserRound
          :size="20"
          aria-hidden="true"
        />
        <span>我的</span>
      </RouterLink>
    </nav>
  </div>
</template>
