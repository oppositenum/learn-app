import { defineStore } from 'pinia'

import {
  ApiError,
  abandonStudentSession,
  completeStudentVoiceExplanation,
  getStudentSession,
  heartbeatStudentSession,
  pauseStudentSession,
  requestStudentSupport,
  resumeStudentSession,
  submitSessionReflection,
  submitStudentAnswer,
  type SessionStatus,
  type SessionTiming,
  type SpeechSegment,
  type StudentSession,
  type StudentSessionTurn,
  type TutorAction,
} from '../api/student'
import { useGrowthStore } from './growth'

export type { TutorAction } from '../api/student'

interface TimelineItem {
  id: string
  actor: 'AI' | 'STUDENT' | 'SYSTEM' | 'TUTOR'
  text: string
  meta?: string
}

let sessionLoadSequence = 0
let sessionRefreshSequence = 0
let pendingResumeOperation: Promise<boolean> | null = null

export const useLearningStore = defineStore('learning', {
  state: () => ({
    requestedSessionID: '',
    sessionID: '',
		version: 0,
		snapshotVersion: 0,
		timingVersion: 0,
    planBlockID: '',
    subject: '',
    knowledgePoint: '',
    difficulty: '',
    startedAt: '',
    targetMinutes: 0,
    prompt: '',
    studentAnswer: '',
    status: '' as SessionStatus | '',
    activeSeconds: 0,
    currentActiveSeconds: 0,
    timingClientAt: 0,
		timingObservedAt: 0,
    tutorAction: 'ASK' as TutorAction,
    socraticRound: 0,
    preparing: false,
    loading: false,
    error: '',
    sessionGone: false,
    voiceAudio: '',
    voiceSegments: [] as SpeechSegment[],
    reflectionStatus: '',
    timeline: [] as TimelineItem[],
		operationSequence: 0,
		timingSequence: 0,
  }),
  actions: {
		reset() {
			sessionLoadSequence++
			sessionRefreshSequence++
			pendingResumeOperation = null
			this.operationSequence++
			this.timingSequence++
			this.requestedSessionID = ''
			this.preparing = false
			this.loading = false
			this.resetSession()
		},
    resetSession() {
      this.sessionID = ''
			this.version = 0
			this.snapshotVersion = 0
			this.timingVersion = 0
      this.planBlockID = ''
      this.subject = ''
      this.knowledgePoint = ''
      this.difficulty = ''
      this.startedAt = ''
      this.targetMinutes = 0
      this.prompt = ''
      this.studentAnswer = ''
      this.status = ''
      this.activeSeconds = 0
      this.currentActiveSeconds = 0
      this.timingClientAt = 0
			this.timingObservedAt = 0
      this.tutorAction = 'ASK'
      this.socraticRound = 0
      this.error = ''
      this.sessionGone = false
      this.voiceAudio = ''
      this.voiceSegments = []
      this.reflectionStatus = ''
      this.timeline = []
    },
		applySession(session: StudentSession) {
			if (this.sessionID === session.id && session.version < this.version) return false
			const observedAt = Date.parse(session.timing_observed_at) || 0
			const applyTiming = this.sessionID !== session.id
				|| session.timing_version > this.timingVersion
				|| (session.timing_version === this.timingVersion && session.status === this.status && observedAt >= this.timingObservedAt)
	      this.sessionID = session.id
			this.version = session.version
			this.snapshotVersion = session.version
      this.planBlockID = session.plan_block_id ?? ''
      this.subject = session.subject_name
      this.knowledgePoint = session.knowledge_point
      this.difficulty = session.difficulty
      this.startedAt = session.started_at
      this.targetMinutes = session.target_minutes
      this.prompt = session.prompt
			if (applyTiming) {
				this.status = session.status
				this.activeSeconds = session.active_seconds
				this.currentActiveSeconds = session.current_active_seconds
				this.timingClientAt = performance.now()
				this.timingObservedAt = observedAt
				this.timingVersion = session.timing_version
			}
      this.socraticRound = session.socratic_round
      this.tutorAction = session.state
      this.voiceAudio = session.voice_audio ?? ''
      this.voiceSegments = session.voice_segments ?? []
	      this.timeline = session.timeline.map((turn: StudentSessionTurn) => ({ id: `${turn.sequence}`, actor: turn.actor, text: turn.message, meta: turn.action }))
			return true
	    },
	    applyTiming(timing: SessionTiming) {
	      if (timing.session_id !== this.sessionID || timing.timing_version < this.timingVersion) return false
			const observedAt = Date.parse(timing.timing_observed_at) || 0
			if (timing.timing_version === this.timingVersion) {
				if (this.status && timing.status !== this.status) return false
				if (observedAt < this.timingObservedAt) return false
			}
			if ((this.status === 'COMPLETED' || this.status === 'ABANDONED') && timing.status !== this.status) return false
			this.timingVersion = timing.timing_version
	      this.status = timing.status
      this.activeSeconds = timing.active_seconds
      this.currentActiveSeconds = timing.current_active_seconds
	      this.timingClientAt = performance.now()
			this.timingObservedAt = observedAt
			return true
	    },
    async loadSession(sessionID: string) {
      const sequence = ++sessionLoadSequence
			const operation = ++this.operationSequence
			this.timingSequence++
			this.loading = false
      this.resetSession()
      this.requestedSessionID = sessionID
      this.preparing = true
      try {
        const session = await getStudentSession(sessionID)
				if (sequence !== sessionLoadSequence || operation !== this.operationSequence || this.requestedSessionID !== sessionID) return false
        this.applySession(session)
        return true
      } catch (error) {
				if (sequence !== sessionLoadSequence || operation !== this.operationSequence || this.requestedSessionID !== sessionID) return false
        if (error instanceof ApiError && error.status === 404) this.sessionGone = true
        this.error = error instanceof Error ? error.message : '课堂暂时不可用'
        return false
      } finally {
				if (sequence === sessionLoadSequence && operation === this.operationSequence && this.requestedSessionID === sessionID) this.preparing = false
      }
	    },
		async refreshSession(requestedSessionID?: string) {
			const sessionID = requestedSessionID || this.sessionID
			if (!sessionID || sessionID !== this.sessionID) return false
			const sequence = ++sessionRefreshSequence
			try {
				const session = await getStudentSession(sessionID)
				if (sequence !== sessionRefreshSequence || sessionID !== this.sessionID) return false
				return this.applySession(session)
			} catch (error) {
				if (sequence !== sessionRefreshSequence || sessionID !== this.sessionID) return false
				if (error instanceof ApiError && error.status === 404) this.sessionGone = true
				return false
			}
		},
    async submitAnswer(sessionID: string, answer: string) {
      const value = answer.trim()
			if (!value || this.sessionID !== sessionID || this.loading) return false
			const operation = ++this.operationSequence
      this.loading = true
      this.error = ''
      try {
	        const result = await submitStudentAnswer(sessionID, value)
					if (operation !== this.operationSequence || this.sessionID !== sessionID) return false
					if (result.version < this.version) return false
					const appendResult = result.version > this.snapshotVersion
        this.studentAnswer = value
	        this.tutorAction = result.action
					this.version = result.version
        this.socraticRound = result.socratic_round
        this.voiceAudio = result.voice_audio ?? ''
        this.voiceSegments = result.voice_segments ?? []
					if (appendResult) {
						this.timeline.push({ id: `pending-${result.version}-student`, actor: 'STUDENT', text: value })
						this.timeline.push({ id: `pending-${result.version}-tutor`, actor: 'TUTOR', text: result.message, meta: result.action })
					}
					if (result.status) {
						this.applyTiming({
							session_id: result.session_id,
							version: result.version,
							timing_version: result.timing_version,
							status: result.status,
							active_seconds: result.active_seconds ?? this.activeSeconds,
							current_active_seconds: result.current_active_seconds ?? (result.status === 'ACTIVE' ? this.currentActiveSeconds : 0),
							timing_observed_at: result.timing_observed_at,
						})
					}
	        if (result.action === 'COMPLETE') {
						this.timingSequence++
	          useGrowthStore().invalidate()
        }
					await this.refreshSession(sessionID)
        return true
      } catch (error) {
				if (operation !== this.operationSequence || this.sessionID !== sessionID) return false
				if (error instanceof ApiError && error.status === 404) this.sessionGone = true
        this.error = error instanceof Error ? error.message : '课堂暂时无法提交'
        return false
      } finally {
				if (operation === this.operationSequence) this.loading = false
      }
    },
    async requestSupport(sessionID: string, type: 'HINT' | 'EXPLAIN') {
			if (this.sessionID !== sessionID || this.loading) return false
			const operation = ++this.operationSequence
      this.loading = true
      this.error = ''
      try {
	        const result = await requestStudentSupport(sessionID, type)
					if (operation !== this.operationSequence || this.sessionID !== sessionID) return false
					if (result.version < this.version) return false
					const appendResult = result.version > this.snapshotVersion
					this.version = result.version
	        this.tutorAction = result.action
					if (appendResult) this.timeline.push({ id: `pending-${result.version}-tutor`, actor: 'TUTOR', text: result.message, meta: result.action })
					if (result.status) this.applyTiming({ session_id: result.session_id, version: result.version, timing_version: result.timing_version, status: result.status, active_seconds: result.active_seconds ?? this.activeSeconds, current_active_seconds: result.current_active_seconds ?? (result.status === 'ACTIVE' ? this.currentActiveSeconds : 0), timing_observed_at: result.timing_observed_at })
					await this.refreshSession(sessionID)
        return true
      } catch (error) {
				if (operation !== this.operationSequence || this.sessionID !== sessionID) return false
				if (error instanceof ApiError && error.status === 404) this.sessionGone = true
        this.error = error instanceof Error ? error.message : '暂时无法生成帮助'
        return false
      } finally {
				if (operation === this.operationSequence) this.loading = false
      }
    },
    async returnFromVoice(sessionID: string) {
			if (this.sessionID !== sessionID || this.loading) return false
			const operation = ++this.operationSequence
      this.loading = true
      this.error = ''
      try {
	        const result = await completeStudentVoiceExplanation(sessionID)
					if (operation !== this.operationSequence || this.sessionID !== sessionID) return false
					if (result.version < this.version) return false
					const appendResult = result.version > this.snapshotVersion
					this.version = result.version
        this.tutorAction = result.action
        this.socraticRound = result.socratic_round
					if (appendResult) this.timeline.push({ id: `pending-${result.version}-tutor`, actor: 'TUTOR', text: result.message, meta: result.action })
					if (result.status) this.applyTiming({ session_id: result.session_id, version: result.version, timing_version: result.timing_version, status: result.status, active_seconds: result.active_seconds ?? this.activeSeconds, current_active_seconds: result.current_active_seconds ?? (result.status === 'ACTIVE' ? this.currentActiveSeconds : 0), timing_observed_at: result.timing_observed_at })
					await this.refreshSession(sessionID)
        return true
      } catch (error) {
				if (operation !== this.operationSequence || this.sessionID !== sessionID) return false
				if (error instanceof ApiError && error.status === 404) this.sessionGone = true
        this.error = error instanceof Error ? error.message : '暂时无法返回原题'
        return false
      } finally {
				if (operation === this.operationSequence) this.loading = false
      }
    },
    async changeLifecycle(transition: (sessionID: string) => Promise<SessionTiming>) {
			if (!this.sessionID) return false
			if (this.loading) {
				this.error = '请等待当前回答处理完成'
				return false
			}
			const sessionID = this.sessionID
			const operation = ++this.operationSequence
			this.timingSequence++
      this.loading = true
      this.error = ''
      try {
				const timing = await transition(sessionID)
				if (operation !== this.operationSequence || this.sessionID !== sessionID) return false
				const applied = this.applyTiming(timing)
				if (timing.version > this.snapshotVersion) await this.refreshSession(sessionID)
				if (!applied && timing.status !== this.status) return false
        return true
      } catch (error) {
				if (operation !== this.operationSequence || this.sessionID !== sessionID) return false
				if (error instanceof ApiError && error.status === 404) this.sessionGone = true
        this.error = error instanceof Error ? error.message : '课堂状态暂时无法同步'
        return false
      } finally {
				if (operation === this.operationSequence) this.loading = false
      }
    },
		async pauseSession(keepalive = false) {
			return this.changeLifecycle((sessionID) => pauseStudentSession(sessionID, keepalive))
	    },
		async pauseForVisibility(keepalive = false) {
			if (!this.sessionID || this.status !== 'ACTIVE') return this.status === 'PAUSED'
			const sessionID = this.sessionID
			const sequence = ++this.timingSequence
			try {
        const timing = await pauseStudentSession(sessionID, keepalive)
        if (sequence !== this.timingSequence || sessionID !== this.sessionID) return false
		const applied = this.applyTiming(timing)
		if (timing.version > this.snapshotVersion) await this.refreshSession(sessionID)
		return applied || timing.status === 'PAUSED'
			} catch {
				return false
			}
			},
		waitForPendingResume() {
			return pendingResumeOperation ?? Promise.resolve(false)
		},
	    resumeSession() {
			if (pendingResumeOperation) return pendingResumeOperation
			const operation = this.changeLifecycle(resumeStudentSession)
			pendingResumeOperation = operation
			void operation.finally(() => {
				if (pendingResumeOperation === operation) pendingResumeOperation = null
			})
			return operation
    },
    async abandonSession() {
      return this.changeLifecycle(abandonStudentSession)
    },
    async heartbeat() {
			if (!this.sessionID || this.status !== 'ACTIVE' || this.loading || document.visibilityState !== 'visible') return
			const sessionID = this.sessionID
				const sequence = ++this.timingSequence
      try {
				const timing = await heartbeatStudentSession(sessionID)
				if (sequence !== this.timingSequence || sessionID !== this.sessionID || this.status !== 'ACTIVE') return
				this.applyTiming(timing)
				if (timing.version > this.snapshotVersion) await this.refreshSession(sessionID)
	      } catch (error) {
				if (error instanceof ApiError && (error.status === 404 || error.status === 409)) await this.refreshSession(sessionID)
	      }
    },
    async reflect(sessionID: string, willingness: 'CONTINUE_TOMORROW' | 'PAUSE' | 'STOP') {
      this.reflectionStatus = '保存中'
      try {
        await submitSessionReflection(sessionID, willingness)
        this.reflectionStatus = willingness === 'CONTINUE_TOMORROW' ? '已记下：明天继续' : willingness === 'PAUSE' ? '已记下：先停一停' : '已记下：不再继续'
      } catch (error) {
        this.reflectionStatus = error instanceof Error ? error.message : '暂时无法保存'
      }
    },
  },
})
