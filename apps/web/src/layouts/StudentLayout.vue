<script setup lang="ts">
import { Home, Sparkles, UserRound } from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { roleHome, useAuthSession } from '../stores/auth'
import { useLearningStore } from '../stores/learning'

const route = useRoute()
const router = useRouter()
const auth = useAuthSession()
const learning = useLearningStore()
const showNavigation = computed(() => !route.meta.hideStudentNav)
let autoPausedSessionID = ''
let stopNavigationGuard: (() => void) | undefined
let heartbeatTimer = 0
let backgroundPausePending = false
let backgroundPauseOperation: Promise<boolean> | null = null
let hiddenReconciliation = Promise.resolve()
let visibleReconciliation: Promise<void> | null = null

function sameClassroomScope(target: typeof route, source: typeof route) {
  return Boolean(target.meta.classroom && source.meta.classroom && String(target.params.id) === String(source.params.id))
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
	autoPausedSessionID = learning.sessionID
	const paused = await pauseForBackground()
	backgroundPausePending = !paused
}

async function reconcileVisible() {
	if (document.visibilityState !== 'visible') return
	if (backgroundPauseOperation) await backgroundPauseOperation.catch(() => false)
	if (document.visibilityState !== 'visible') return
	if (!await syncStudentIdentity()) return
	if (document.visibilityState !== 'visible') return
	if (route.meta.classroom && learning.sessionID) await learning.refreshSession()
	if (document.visibilityState !== 'visible') return
	backgroundPausePending = false
	if (autoPausedSessionID && autoPausedSessionID === learning.sessionID && route.meta.classroom && learning.status === 'PAUSED') {
		const resumed = await learning.resumeForVisibility()
		if (document.visibilityState !== 'visible') {
			await reconcileHidden()
			return
		}
		if (resumed) autoPausedSessionID = ''
	} else if (autoPausedSessionID === learning.sessionID && learning.status === 'ACTIVE') {
		autoPausedSessionID = ''
	}
}

function queueVisibleReconciliation() {
	if (!visibleReconciliation) {
		visibleReconciliation = Promise.resolve().then(reconcileVisible).finally(() => {
			visibleReconciliation = null
		})
	}
	return visibleReconciliation
}

function handleVisibility() {
	if (document.visibilityState === 'visible') {
		void queueVisibleReconciliation()
		return
	}
	hiddenReconciliation = hiddenReconciliation.then(() => reconcileHidden(), () => reconcileHidden())
}

function handlePageHide() {
	if (!route.meta.classroom || learning.status !== 'ACTIVE' || !learning.sessionID) return
  const sessionID = learning.sessionID
  autoPausedSessionID = sessionID
	void pauseForBackground().catch(() => undefined)
}

function handlePageShow(event: PageTransitionEvent) {
	if (event.persisted && document.visibilityState === 'visible') void queueVisibleReconciliation()
}

const stopLearningWatch = watch(
	() => [learning.sessionID, learning.status, learning.loading, learning.preparing],
	() => {
		if (backgroundPausePending || document.visibilityState === 'hidden') handleVisibility()
	},
)

onMounted(() => {
  document.addEventListener('visibilitychange', handleVisibility)
  window.addEventListener('pagehide', handlePageHide)
  window.addEventListener('pageshow', handlePageShow)
  heartbeatTimer = window.setInterval(() => {
    if (route.meta.classroom && document.visibilityState === 'visible') void learning.heartbeat()
  }, 30_000)
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
