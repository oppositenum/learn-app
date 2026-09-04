import { onMounted, onUnmounted } from 'vue'
import { useRoute } from 'vue-router'

import { useSupervisionStore } from '../../stores/supervision'

export function useParentLiveConnection() {
  const route = useRoute()
	const supervision = useSupervisionStore()
	let elapsedTimer: number | undefined

	function updateElapsed() {
		const running = supervision.sessionActive && supervision.timingClientAt > 0
			? Math.max(0, Math.floor((performance.now() - supervision.timingClientAt) / 1000))
			: 0
		const seconds = Math.max(0, supervision.activeSeconds + running)
		supervision.elapsed = `${String(Math.floor(seconds / 60)).padStart(2, '0')}:${String(seconds % 60).padStart(2, '0')}`
	}

	onMounted(async () => {
    const studentID = typeof route.query.student === 'string' ? route.query.student : ''
    const sessionID = typeof route.query.session === 'string' ? route.query.session : ''
		await supervision.initialize(studentID, sessionID)
		updateElapsed()
		elapsedTimer = window.setInterval(updateElapsed, 1000)
	  })

	onUnmounted(() => {
		if (elapsedTimer !== undefined) window.clearInterval(elapsedTimer)
		supervision.disconnect()
	})

  return supervision
}
