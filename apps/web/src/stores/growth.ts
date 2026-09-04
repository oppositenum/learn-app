import { defineStore } from 'pinia'

import { getStudentGrowth, type StudentGrowth } from '../api/student'

let growthRequestSequence = 0
let growthRefreshTimer = 0

export function normalizeLearningDate(value = '') {
	const match = /^(\d{4}-\d{2}-\d{2})/.exec(value)
	return match?.[1] ?? ''
}

export function shanghaiLearningDate(now = new Date()) {
	const parts = new Intl.DateTimeFormat('en', {
		timeZone: 'Asia/Shanghai',
		year: 'numeric',
		month: '2-digit',
		day: '2-digit',
	}).formatToParts(now)
	const value = Object.fromEntries(parts.map((part) => [part.type, part.value]))
	return `${value.year}-${value.month}-${value.day}`
}

function millisecondsUntilNextLearningDate(now = new Date()) {
  const [year, month, day] = shanghaiLearningDate(now).split('-').map(Number)
  const nextMidnight = Date.UTC(year, month - 1, day + 1) - 8 * 60 * 60 * 1000
  return Math.max(1_000, nextMidnight - now.getTime() + 1_000)
}

export const useGrowthStore = defineStore('growth', {
	state: () => ({
		data: null as StudentGrowth | null,
		studentID: '',
		status: 'idle' as 'idle' | 'loading' | 'ready' | 'error',
		error: '',
		stale: false,
		loadedLearningDate: '',
		requestSequence: 0,
	}),
	actions: {
		async load(force = false, studentID = '') {
      if (studentID && this.studentID !== studentID) {
        window.clearTimeout(growthRefreshTimer)
        growthRefreshTimer = 0
        this.$reset()
				this.studentID = studentID
			}
		const currentLearningDate = shanghaiLearningDate()
		if (!force && (this.status === 'loading' || (this.status === 'ready' && !this.stale && this.loadedLearningDate === currentLearningDate))) return
			const requestSequence = ++growthRequestSequence
			this.requestSequence = requestSequence
			const requestedStudentID = this.studentID
			this.status = 'loading'
			this.error = ''
			try {
				const data = await getStudentGrowth()
				if (this.requestSequence !== requestSequence || this.studentID !== requestedStudentID) return
				if (requestedStudentID && data.student_id !== requestedStudentID) {
					this.status = 'error'
					this.error = '成长数据与当前学生不一致'
					return
				}
				this.data = data
				this.status = 'ready'
				this.stale = false
				this.loadedLearningDate = data.learning_date
				window.clearTimeout(growthRefreshTimer)
				growthRefreshTimer = window.setTimeout(() => {
					this.invalidate()
					void this.load(true, this.studentID)
				}, millisecondsUntilNextLearningDate())
			} catch (error) {
				if (this.requestSequence !== requestSequence || this.studentID !== requestedStudentID) return
				this.status = 'error'
				this.error = error instanceof Error ? error.message : '成长数据暂时不可用'
			}
		},
		invalidate() {
			this.stale = true
			if (this.status === 'loading') {
				this.requestSequence = ++growthRequestSequence
				this.status = 'idle'
			}
		},
		reset() {
			window.clearTimeout(growthRefreshTimer)
			growthRefreshTimer = 0
			this.$reset()
		},
	},
})
