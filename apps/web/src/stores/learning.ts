import { defineStore } from 'pinia'

import {
  ApiError,
  abandonStudentSession,
  completeStudentVoiceExplanation,
  getStudentSession,
  heartbeatStudentSession,
  pauseStudentSession,
  requestStudentSupport,
  requestStudentStageSupport,
  resumeStudentSession,
  sessionResumeRequiredCode,
  submitSessionReflection,
  submitStudentAnswer,
  submitStudentStageResponse,
  type StageIdentity,
	  type SessionStatus,
	type SessionTiming,
	type SafetyNotice,
  type SpeechSegment,
  type StudentSession,
  type StudentInteraction,
  type StudentStageFlow,
  type StudentSessionTurn,
  type TutorAction,
} from '../api/student'
import { initialStructuredResponse, isStructuredInteraction, newOperationID, stageIdentity, stageResponseKey, stageTaskKey } from '../lib/studentInteraction'
import { useGrowthStore } from './growth'

export type { TutorAction } from '../api/student'

export type SupportRequestResult =
	| { requestSent: false; reason: 'SESSION_MISMATCH' | 'LOADING' | 'SESSION_INACTIVE' }
	| { requestSent: true; outcome: 'APPLIED' | 'FAILED' | 'STALE' }

interface TimelineItem {
  id: string
  actor: 'AI' | 'STUDENT' | 'SYSTEM' | 'TUTOR'
  text: string
  meta?: string
}

interface StudentSafetyNotice extends SafetyNotice {
	message: string
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
    questionID: '',
    interaction: null as StudentInteraction | null,
    stageFlow: null as StudentStageFlow | null,
    studentAnswer: '',
    answerDraftSessionID: '',
    answerDraft: '',
    status: '' as SessionStatus | '',
    activeSeconds: 0,
    currentActiveSeconds: 0,
    timingClientAt: 0,
		timingObservedAt: 0,
    tutorAction: 'ASK' as TutorAction,
    socraticRound: 0,
    preparing: false,
    loading: false,
    submissionInFlight: false,
    error: '',
    sessionGone: false,
    voiceAudio: '',
    voiceSegments: [] as SpeechSegment[],
	    reflectionStatus: '',
	    safetyNotice: null as StudentSafetyNotice | null,
    timeline: [] as TimelineItem[],
		operationSequence: 0,
		timingSequence: 0,
		structuredDraftKey: '',
		structuredDraft: {} as Record<string, unknown>,
		stageAnswerOperationKey: '',
		stageAnswerOperationID: '',
		stageSupportOperationKey: '',
		stageSupportOperationID: '',
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
			this.submissionInFlight = false
			this.answerDraftSessionID = ''
			this.answerDraft = ''
			this.structuredDraftKey = ''
			this.structuredDraft = {}
			this.clearStageOperations()
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
      this.questionID = ''
      this.interaction = null
      this.stageFlow = null
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
			this.submissionInFlight = false
	      this.reflectionStatus = ''
	      this.safetyNotice = null
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
			this.questionID = session.question_id
			this.interaction = session.interaction ?? null
			this.stageFlow = session.stage_flow ?? null
			const identity = stageIdentity(session.id, session.question_id, session.stage_flow)
			if (identity && session.interaction && isStructuredInteraction(session.interaction, session.stage_flow)) {
				const key = stageTaskKey(session.id, identity)
				if (this.structuredDraftKey !== key) {
					this.structuredDraftKey = key
					this.structuredDraft = initialStructuredResponse(session.interaction)
					this.clearStageOperations()
				}
			} else {
				this.structuredDraftKey = ''
				this.structuredDraft = {}
				this.clearStageOperations()
			}
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
			setAnswerDraft(sessionID: string, value: string) {
				this.answerDraftSessionID = sessionID
				this.answerDraft = value
			},
			answerDraftFor(sessionID: string) {
				return this.answerDraftSessionID === sessionID ? this.answerDraft : ''
			},
			setStructuredDraft(key: string, value: Record<string, unknown>) {
				if (this.structuredDraftKey === key) this.structuredDraft = value
			},
			clearStageOperations() {
				this.stageAnswerOperationKey = ''
				this.stageAnswerOperationID = ''
				this.stageSupportOperationKey = ''
				this.stageSupportOperationID = ''
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
			if (this.answerDraftSessionID && this.answerDraftSessionID !== sessionID) {
				this.answerDraftSessionID = ''
				this.answerDraft = ''
			}
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
	    async submitAnswer(sessionID: string, answer: string, retried = false): Promise<boolean> {
      const value = answer.trim()
			if (!value || this.sessionID !== sessionID || this.loading) return false
			const operation = ++this.operationSequence
	      this.loading = true
			this.submissionInFlight = true
	      this.error = ''
	      this.safetyNotice = null
	      try {
		        const result = await submitStudentAnswer(sessionID, value)
					if (operation !== this.operationSequence || this.sessionID !== sessionID) return false
					if (result.version < this.version) return false
					const appendResult = result.version > this.snapshotVersion
						if (result.safety) {
							this.studentAnswer = ''
							this.safetyNotice = { ...result.safety, message: result.message }
						} else {
							this.studentAnswer = value
						}
	        this.tutorAction = result.action
					this.version = result.version
	        this.socraticRound = result.socratic_round ?? this.socraticRound
        this.voiceAudio = result.voice_audio ?? ''
        this.voiceSegments = result.voice_segments ?? []
						if (appendResult && !result.safety) {
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
				// Idle-session recovery paused the classroom while the child was
				// thinking. Resume once and resubmit rather than telling a student
				// their answer could not be sent.
				if (error instanceof ApiError && error.code === sessionResumeRequiredCode && !retried) {
					this.loading = false
					this.submissionInFlight = false
					// Resume reports success through several timing conditions, so do
					// not gate the retry on its return value; the retried flag already
					// bounds this to a single extra attempt.
					await this.resumeSession()
					if (this.sessionID === sessionID) {
						this.loading = false
						this.submissionInFlight = false
						return await this.submitAnswer(sessionID, value, true)
					}
				}
        this.error = error instanceof Error ? error.message : '课堂暂时无法提交'
        return false
      } finally {
				if (operation === this.operationSequence) {
					this.loading = false
					this.submissionInFlight = false
				}
	      }
	    },
		async submitStageResponse(sessionID: string, response: Record<string, unknown>) {
			const identity = stageIdentity(this.sessionID, this.questionID, this.stageFlow ?? undefined)
			if (!identity || this.sessionID !== sessionID || this.loading || !this.interaction || !isStructuredInteraction(this.interaction, this.stageFlow ?? undefined)) return false
			const taskKey = stageTaskKey(sessionID, identity)
			if (this.structuredDraftKey !== taskKey) return false
			const operationKey = stageResponseKey(sessionID, identity, response)
			if (this.stageAnswerOperationKey !== operationKey) {
				this.stageAnswerOperationKey = operationKey
				this.stageAnswerOperationID = newOperationID()
			}
			const operation = ++this.operationSequence
			this.loading = true
			this.submissionInFlight = true
			this.error = ''
			this.safetyNotice = null
			try {
				const result = await submitStudentStageResponse(sessionID, identity as StageIdentity, response, this.stageAnswerOperationID)
				if (operation !== this.operationSequence || this.sessionID !== sessionID) return false
				if (result.version < this.version) return false
				this.version = result.version
				this.tutorAction = result.action
				if (result.safety) {
					this.safetyNotice = { ...result.safety, message: result.message }
					this.structuredDraft = {}
				} else if (result.version > this.snapshotVersion) {
					this.timeline.push({ id: `pending-${result.version}-tutor`, actor: 'TUTOR', text: result.message, meta: result.action })
				}
				if (result.status) this.applyTiming({ session_id: result.session_id, version: result.version, timing_version: result.timing_version, status: result.status, active_seconds: result.active_seconds ?? this.activeSeconds, current_active_seconds: result.current_active_seconds ?? (result.status === 'ACTIVE' ? this.currentActiveSeconds : 0), timing_observed_at: result.timing_observed_at })
				if (result.action === 'COMPLETE') {
					this.timingSequence++
					useGrowthStore().invalidate()
				}
				if (!await this.refreshSession(sessionID)) {
					this.error = '新任务暂时没有载入，请再次提交以恢复课堂'
					return false
				}
				this.clearStageOperations()
				return true
			} catch (error) {
				if (operation !== this.operationSequence || this.sessionID !== sessionID) return false
				if (error instanceof ApiError && error.status === 404) this.sessionGone = true
				if (error instanceof ApiError && error.status === 409) {
					const reconciled = await this.refreshSession(sessionID)
					if (reconciled) {
						this.clearStageOperations()
						this.error = '课堂状态已更新，请在当前任务继续'
						return false
					}
				}
				this.error = error instanceof Error ? error.message : '课堂暂时无法提交'
				return false
			} finally {
				if (operation === this.operationSequence) {
					this.loading = false
					this.submissionInFlight = false
				}
			}
		},
	    async requestSupport(sessionID: string, type: 'HINT' | 'EXPLAIN') {
			if (this.sessionID !== sessionID) {
				this.error = '课堂内容已经更新，请重新加载课堂后再试'
				return { requestSent: false, reason: 'SESSION_MISMATCH' } as SupportRequestResult
			}
			if (this.loading) {
				this.error = '老师正在准备回应，请稍等'
				return { requestSent: false, reason: 'LOADING' } as SupportRequestResult
			}
			if (this.status !== 'ACTIVE') {
				this.error = this.status === 'PAUSED'
					? '这次探索已暂停，请先点击「继续探索」'
					: '这次探索暂时不能请求帮助，请重新加载课堂'
				return { requestSent: false, reason: 'SESSION_INACTIVE' } as SupportRequestResult
			}
			const operation = ++this.operationSequence
      this.loading = true
      this.error = ''
      try {
	        const result = await requestStudentSupport(sessionID, type)
					if (operation !== this.operationSequence || this.sessionID !== sessionID) return { requestSent: true, outcome: 'STALE' } as SupportRequestResult
					if (result.version < this.version) return { requestSent: true, outcome: 'STALE' } as SupportRequestResult
					const appendResult = result.version > this.snapshotVersion
					this.version = result.version
	        this.tutorAction = result.action
					if (appendResult) this.timeline.push({ id: `pending-${result.version}-tutor`, actor: 'TUTOR', text: result.message, meta: result.action })
					if (result.status) this.applyTiming({ session_id: result.session_id, version: result.version, timing_version: result.timing_version, status: result.status, active_seconds: result.active_seconds ?? this.activeSeconds, current_active_seconds: result.current_active_seconds ?? (result.status === 'ACTIVE' ? this.currentActiveSeconds : 0), timing_observed_at: result.timing_observed_at })
					await this.refreshSession(sessionID)
					this.error = ''
	        return { requestSent: true, outcome: 'APPLIED' } as SupportRequestResult
      } catch (error) {
					if (operation !== this.operationSequence || this.sessionID !== sessionID) return { requestSent: true, outcome: 'STALE' } as SupportRequestResult
					if (error instanceof ApiError && error.status === 404) this.sessionGone = true
        this.error = error instanceof Error ? error.message : '暂时无法生成帮助'
        return { requestSent: true, outcome: 'FAILED' } as SupportRequestResult
      } finally {
				if (operation === this.operationSequence) this.loading = false
	      }
	    },
	    async requestStageSupport(sessionID: string, type: 'HINT' | 'EXPLAIN') {
			const identity = stageIdentity(this.sessionID, this.questionID, this.stageFlow ?? undefined)
			if (!identity || this.sessionID !== sessionID || this.loading) return false
			const key = `${stageTaskKey(sessionID, identity)}:${type}`
			if (this.stageSupportOperationKey !== key) {
				this.stageSupportOperationKey = key
				this.stageSupportOperationID = newOperationID()
			}
			const operation = ++this.operationSequence
			this.loading = true
			this.error = ''
			try {
				const result = await requestStudentStageSupport(sessionID, identity, type, this.stageSupportOperationID)
				if (operation !== this.operationSequence || this.sessionID !== sessionID) return false
				if (result.version < this.version) return false
				this.version = result.version
				this.tutorAction = result.action
				if (result.version > this.snapshotVersion) this.timeline.push({ id: `pending-${result.version}-tutor`, actor: 'TUTOR', text: result.message, meta: result.action })
				if (result.status) this.applyTiming({ session_id: result.session_id, version: result.version, timing_version: result.timing_version, status: result.status, active_seconds: result.active_seconds ?? this.activeSeconds, current_active_seconds: result.current_active_seconds ?? (result.status === 'ACTIVE' ? this.currentActiveSeconds : 0), timing_observed_at: result.timing_observed_at })
				if (!await this.refreshSession(sessionID)) {
					this.error = '课堂状态暂时没有载入，请再次请求帮助'
					return false
				}
				this.stageSupportOperationKey = ''
				this.stageSupportOperationID = ''
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
	        this.socraticRound = result.socratic_round ?? this.socraticRound
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
			if (!this.sessionID || this.status !== 'ACTIVE' || (this.loading && !this.submissionInFlight) || document.visibilityState !== 'visible') return
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
