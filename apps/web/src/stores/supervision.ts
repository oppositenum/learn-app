import { defineStore } from 'pinia'

import { getParentChildren, getParentLiveSession, sendParentIntervention, type ParentChild } from '../api/parent'

export interface ParentTimelineItem {
  id: string
  actor: 'AI' | 'STUDENT' | 'SYSTEM' | 'TUTOR'
  text: string
  meta?: string
}

export interface ParentRealtimeMessage {
  event_id: string
  student_id?: string
  session_id: string
  type: string
  payload: Record<string, unknown>
}

let parentSocket: WebSocket | undefined
let socketStudentID = ''
let socketGeneration = 0
let reconnectAttempt = 0
let reconnectTimer: number | undefined
let refreshTimer: number | undefined
let requestGeneration = 0

export const useSupervisionStore = defineStore('supervision', {
  state: () => ({
    children: [] as ParentChild[],
    connected: false,
    reconnecting: false,
    sessionActive: false,
		studentID: '', sessionID: '', childName: '', startedAt: '', subject: '', knowledgePoint: '', elapsed: '00:00', target: '00:00', activeSeconds: 0, timingClientAt: 0,
    studentAnswerPreview: '', answerVisibility: 'NONE' as 'NONE' | 'SHORT_CURRENT' | 'WITHHELD_LONG' | 'WITHHELD_NOT_ACTIVE', correctAnswer: '', misconception: '', tutorAction: '', tutorReason: '', socraticRound: 0,
    masteryState: 'UNKNOWN', masteryScore: 0,
    timeline: [] as ParentTimelineItem[],
    connectionError: '',
    interventionStatus: '',
  }),
  actions: {
    async initialize(preferredStudentID = '', preferredSessionID = '') {
      const generation = ++requestGeneration
      this.connectionError = ''
      try {
        const children = await getParentChildren()
        if (generation !== requestGeneration) return
        this.children = children
        if (this.children.length === 0) {
          this.disconnect()
          this.connectionError = '尚未绑定孩子账户'
          return
        }
        const preferred = this.children.find((child) => child.student_id === preferredStudentID)
        const current = this.children.find((child) => child.student_id === this.studentID)
        const latestActive = [...this.children]
          .filter((child) => child.active_session_id)
          .sort((left, right) => Date.parse(right.started_at ?? '') - Date.parse(left.started_at ?? ''))[0]
        const child = preferred ?? current ?? latestActive ?? this.children[0]
        await this.selectChild(child.student_id, preferredSessionID || child.active_session_id || '', generation)
      } catch (error) {
        if (generation !== requestGeneration) return
        this.connectionError = error instanceof Error ? error.message : '孩子状态暂时不可用'
      }
    },

    async selectChild(studentID: string, sessionID = '', generation = ++requestGeneration) {
      if (generation !== requestGeneration) return
      const child = this.children.find((item) => item.student_id === studentID)
      if (!child) return
      const switched = this.studentID !== studentID
      this.studentID = studentID
      this.childName = child.display_name
      this.connectionError = ''
      if (switched) this.clearSession()
      this.openRealtime(studentID)
      const activeSessionID = sessionID || child.active_session_id || ''
      if (activeSessionID) await this.refreshSession(studentID, activeSessionID, generation)
      else this.clearSession()
    },

    async refreshSession(studentID: string, sessionID: string, generation = requestGeneration) {
      if (!studentID || !sessionID || studentID !== this.studentID) return
      try {
        const live = await getParentLiveSession(studentID, sessionID)
        if (generation !== requestGeneration || studentID !== this.studentID) return
        this.sessionID = sessionID
        this.sessionActive = live.status === 'ACTIVE'
        this.subject = live.subject
        this.knowledgePoint = live.knowledge_point
        this.studentAnswerPreview = live.student_answer_preview ?? ''
        this.answerVisibility = live.student_answer_visibility
        this.correctAnswer = formatAnswer(live.correct_answer)
        this.misconception = live.error_type || live.misconceptions.join('、')
        this.tutorAction = live.tutor_action || live.current_state
        this.tutorReason = formatTutorReason(live.tutor_reason)
        this.socraticRound = live.socratic_round
        this.startedAt = live.started_at
				this.activeSeconds = live.active_seconds
				this.timingClientAt = performance.now()
        this.target = `${String(live.target_minutes).padStart(2, '0')}:00`
        this.masteryState = live.mastery_state
        this.masteryScore = live.mastery_score
        this.timeline = live.timeline.map((turn) => ({ id: `${turn.sequence}`, actor: turn.actor, text: turn.message, meta: turn.action || new Date(turn.at).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' }) }))
      } catch (error) {
        if (generation !== requestGeneration) return
        this.connectionError = error instanceof Error ? error.message : '课堂状态暂时不可用'
      }
    },

    clearSession() {
      this.sessionID = ''
      this.sessionActive = false
      this.startedAt = ''
      this.subject = ''
      this.knowledgePoint = ''
      this.elapsed = '00:00'
      this.target = '00:00'
			this.activeSeconds = 0
			this.timingClientAt = 0
      this.studentAnswerPreview = ''
      this.answerVisibility = 'NONE'
      this.correctAnswer = ''
      this.misconception = ''
      this.tutorAction = ''
      this.tutorReason = ''
      this.socraticRound = 0
      this.masteryState = 'UNKNOWN'
      this.masteryScore = 0
      this.timeline = []
    },

    openRealtime(studentID: string) {
      if (!studentID) return
      if (socketStudentID === studentID && parentSocket && (parentSocket.readyState === WebSocket.CONNECTING || parentSocket.readyState === WebSocket.OPEN)) return
      socketGeneration += 1
      const generation = socketGeneration
      if (reconnectTimer !== undefined) window.clearTimeout(reconnectTimer)
      parentSocket?.close(1000, 'switching student')
      socketStudentID = studentID
      this.connected = false
      this.reconnecting = false
      const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
      const socket = new WebSocket(`${protocol}//${window.location.host}/ws/parent/${encodeURIComponent(studentID)}`)
      parentSocket = socket

      socket.addEventListener('open', () => {
        if (generation !== socketGeneration) return
        reconnectAttempt = 0
        this.connected = true
        this.reconnecting = false
        this.connectionError = ''
      })
      socket.addEventListener('message', (event) => {
        if (generation !== socketGeneration) return
        let message: ParentRealtimeMessage
        try { message = JSON.parse(String(event.data)) as ParentRealtimeMessage }
        catch { return }
        if (message.student_id && message.student_id !== this.studentID) return
        this.applyRealtimeMessage(message)
        if (message.session_id) this.scheduleSessionRefresh(message.session_id)
      })
      socket.addEventListener('close', () => {
        if (generation !== socketGeneration || studentID !== this.studentID) return
        this.connected = false
        this.reconnecting = true
        this.connectionError = '实时连接已断开，正在自动重连'
        const delay = Math.min(10_000, 500 * 2 ** reconnectAttempt)
        reconnectAttempt += 1
        reconnectTimer = window.setTimeout(() => {
          if (generation !== socketGeneration || studentID !== this.studentID) return
          socketStudentID = ''
          this.openRealtime(studentID)
        }, delay)
      })
    },

    applyRealtimeMessage(message: ParentRealtimeMessage) {
      const payload = message.payload
      const child = this.children.find((item) => item.student_id === this.studentID)
      if (message.session_id && message.session_id !== this.sessionID) {
        this.clearSession()
        this.sessionID = message.session_id
        this.sessionActive = true
      }
      if (child && message.type === 'SESSION_STARTED') child.active_session_id = message.session_id
      if (typeof payload.subject === 'string') this.subject = payload.subject
      if (typeof payload.knowledge_point === 'string') this.knowledgePoint = payload.knowledge_point
      if (typeof payload.target_minutes === 'number') this.target = `${String(payload.target_minutes).padStart(2, '0')}:00`
			if (typeof payload.active_seconds === 'number') {
				this.activeSeconds = payload.active_seconds
				this.timingClientAt = performance.now()
			}
      if (typeof payload.correct_answer !== 'undefined') this.correctAnswer = formatAnswer(payload.correct_answer)
      if (typeof payload.error_type === 'string') this.misconception = payload.error_type
      if (typeof payload.misconception === 'string') this.misconception = payload.misconception
      if (typeof payload.action === 'string') this.tutorAction = payload.action
      if (typeof payload.tutor_action === 'string') this.tutorAction = payload.tutor_action
      if (typeof payload.round === 'number') this.socraticRound = payload.round
      if (typeof payload.mastery_state === 'string') this.masteryState = payload.mastery_state
      if (typeof payload.mastery_score === 'number') this.masteryScore = payload.mastery_score
      if (typeof payload.reason === 'string') this.tutorReason = formatTutorReason(payload.reason)
			if (message.type === 'SESSION_RESUMED') this.sessionActive = true
			if (message.type === 'SESSION_PAUSED' || message.type === 'SESSION_ABANDONED' || message.type === 'SESSION_COMPLETED') {
        this.sessionActive = false
				if (child && (message.type === 'SESSION_ABANDONED' || message.type === 'SESSION_COMPLETED')) child.active_session_id = null
      }
      this.timeline.push({
        id: message.event_id,
        actor: message.type === 'ANSWER_SUBMITTED' ? 'STUDENT' : message.type === 'ANSWER_ANALYZED' ? 'SYSTEM' : 'TUTOR',
        text: realtimeTimelineText(message.type, payload),
        meta: message.type,
      })
    },

    scheduleSessionRefresh(sessionID: string) {
      if (refreshTimer !== undefined) window.clearTimeout(refreshTimer)
      refreshTimer = window.setTimeout(() => this.refreshSession(this.studentID, sessionID), 80)
    },

    disconnect() {
      socketGeneration += 1
      if (reconnectTimer !== undefined) window.clearTimeout(reconnectTimer)
      if (refreshTimer !== undefined) window.clearTimeout(refreshTimer)
      reconnectTimer = undefined
      refreshTimer = undefined
      reconnectAttempt = 0
      parentSocket?.close(1000, 'parent view closed')
      parentSocket = undefined
      socketStudentID = ''
      this.connected = false
      this.reconnecting = false
    },

    reset() {
      requestGeneration++
      this.disconnect()
      this.$reset()
    },

    async intervene(type: 'ENCOURAGEMENT' | 'REDUCE_INTENSITY' | 'REVIEW_ONLY' | 'STATE_NOT_GOOD') {
      if (!this.studentID) return
			const generation = requestGeneration
      this.interventionStatus = '保存中'
      try {
				await sendParentIntervention(this.studentID, type)
				if (generation === requestGeneration) this.interventionStatus = '已同步'
			} catch (error) {
				if (generation === requestGeneration) this.interventionStatus = error instanceof Error ? error.message : '操作失败'
			}
    },
  },
})

function formatAnswer(answer: unknown): string {
  if (typeof answer === 'string' || typeof answer === 'number') return String(answer)
  if (answer && typeof answer === 'object' && 'value' in answer) return String(answer.value)
  return JSON.stringify(answer)
}

function eventLabel(type: string): string {
  const labels: Record<string, string> = {
    SESSION_STARTED: '孩子开始了新的课堂',
    QUESTION_PRESENTED: '系统已展示新问题',
    ANSWER_ANALYZED: '系统已完成回答分析',
    TUTOR_ACTION_SELECTED: 'AI 已选择下一步教学动作',
    SESSION_COMPLETED: '本次课堂已完成',
  }
  return labels[type] || type
}

function realtimeTimelineText(type: string, payload: Record<string, unknown>): string {
  if (typeof payload.message === 'string') return payload.message
  return eventLabel(type)
}

export function formatTutorReason(reason: string): string {
  const reasons: Record<string, string> = {
    'inspect the current reasoning': '先检查孩子当前思路，定位卡住的具体一步。',
    'split the task into a smaller step': '当前任务跨度较大，先拆成一个更小的步骤。',
    'switch to a concrete life analogy': '前两轮仍未突破，改用更具体的生活类比。',
    'Socratic failed-round limit reached; explain with a parallel example': '三轮有效启发已到上限，停止追问并切换平行例子讲解。',
    'emotion signal takes priority over further probing': '检测到低投入或厌烦信号，暂停继续追问。',
    'deescalate with a short voice explanation': '检测到挫败信号，降低强度并改用短语音讲解。',
    'remediate a prerequisite and preserve the original task': '发现底层知识缺口，保留原任务并先补前置知识。',
    'student requested one bounded hint': '孩子主动请求一次有限提示，不消耗启发轮次。',
    'student requested a parallel example without the original answer': '孩子表示不会，切换到不泄露原题答案的平行讲解。',
    'cross-subject prerequisite verified; original task restored': '跨学科前置知识已验证，恢复原任务。',
    'rules engine verified spaced mastery': '规则引擎已核验独立证据与跨天复习证据。',
  }
  return reasons[reason] || reason
}
