export type GrowthIndicatorCode = 'INDEPENDENT_SOLVING' | 'UNDERSTANDING_AFTER_HELP' | 'SELF_CORRECTION' | 'TRANSFER_SUCCESS' | 'DELAYED_REVIEW'

export interface GrowthEvidenceEvent {
  source_kind: string
  source_id: string
  subject: string
  knowledge_point: string
  stage?: string
  evidence_form?: string
  occurred_at: string
}

export interface GrowthIndicator {
  code: GrowthIndicatorCode
  label: string
  count: number
  events: GrowthEvidenceEvent[]
  events_truncated: boolean
}

export interface GrowthEvidenceProjection {
  policy_version: 'growth-evidence-v1'
  indicators: GrowthIndicator[]
}
