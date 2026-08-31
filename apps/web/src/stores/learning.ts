import { defineStore } from 'pinia'

import { completeStudentVoiceExplanation, getStudentSession, requestStudentSupport, submitSessionReflection, submitStudentAnswer, type SpeechSegment, type StudentSessionTurn } from '../api/student'

export type TutorAction = 'INTRO' | 'ASK' | 'WAIT' | 'ANALYZE' | 'PROBE' | 'HINT' | 'SCAFFOLD' | 'ANALOGY' | 'BACKTRACK' | 'EXPLAIN' | 'VOICE_EXPLAIN' | 'RETURN' | 'VARIANT' | 'ABSTRACT' | 'VERIFY' | 'REVIEW' | 'BREAK' | 'COMPLETE'

interface TimelineItem {
  id: string
  actor: 'AI' | 'STUDENT' | 'SYSTEM' | 'TUTOR'
  text: string
  meta?: string
}

export const useLearningStore = defineStore('learning', {
  state: () => ({
		sessionID: '', subject: '', knowledgePoint: '', difficulty: '', startedAt: '', targetMinutes: 0, prompt: '', studentAnswer: '',
		tutorAction: 'ASK' as TutorAction,
		socraticRound: 0,
		loading: false,
		error: '',
		voiceAudio: '',
		voiceSegments: [] as SpeechSegment[],
		reflectionStatus: '',
		timeline: [] as TimelineItem[],
  }),
  actions: {
		async loadSession(sessionID: string) {
			this.loading = true
			this.error = ''
			try {
				const session = await getStudentSession(sessionID)
				this.sessionID = session.id
				this.subject = session.subject_name
				this.knowledgePoint = session.knowledge_point
				this.difficulty = session.difficulty
				this.startedAt = session.started_at
				this.targetMinutes = session.target_minutes
				this.prompt = session.prompt
				this.socraticRound = session.socratic_round
				this.tutorAction = session.state as TutorAction
				this.voiceAudio = session.voice_audio ?? ''
				this.voiceSegments = session.voice_segments ?? []
				this.timeline = session.timeline.map((turn: StudentSessionTurn) => ({ id: `${turn.sequence}`, actor: turn.actor, text: turn.message, meta: turn.action }))
			} catch (error) {
				this.error = error instanceof Error ? error.message : '课堂暂时不可用'
			} finally {
				this.loading = false
			}
		},
		async submitAnswer(sessionID: string, answer: string) {
      const text = answer.trim()
      if (!text) return
			this.loading = true
			this.error = ''
      this.studentAnswer = text
      this.timeline.push({ id: crypto.randomUUID(), actor: 'STUDENT', text })
			try {
				const result = await submitStudentAnswer(sessionID, text)
				if (result.action === 'RETURN' || result.action === 'BACKTRACK') {
					await this.loadSession(sessionID)
					return
				}
				this.tutorAction = result.action
				this.socraticRound = result.socratic_round
				this.voiceAudio = result.voice_audio ?? ''
				this.voiceSegments = result.voice_segments ?? []
				this.timeline.push({ id: crypto.randomUUID(), actor: 'TUTOR', text: result.message, meta: `${result.action} · ${result.socratic_round}/3` })
			} catch (error) {
				this.error = error instanceof Error ? error.message : '课堂暂时无法提交'
			} finally {
				this.loading = false
			}
    },
		async requestSupport(sessionID: string, type: 'HINT' | 'EXPLAIN') {
			this.loading = true
			this.error = ''
			try {
				const result = await requestStudentSupport(sessionID, type)
				this.tutorAction = result.action
				this.timeline.push({ id: crypto.randomUUID(), actor: 'TUTOR', text: result.message, meta: result.action })
			} catch (error) {
				this.error = error instanceof Error ? error.message : '暂时无法生成帮助'
			} finally {
				this.loading = false
			}
		},
		async returnFromVoice(sessionID: string) {
			this.loading = true
			this.error = ''
			try {
				const result = await completeStudentVoiceExplanation(sessionID)
				this.tutorAction = result.action
				this.socraticRound = result.socratic_round
				this.timeline.push({ id: crypto.randomUUID(), actor: 'TUTOR', text: result.message, meta: result.action })
			} catch (error) {
				this.error = error instanceof Error ? error.message : '暂时无法返回原题'
			} finally {
				this.loading = false
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
