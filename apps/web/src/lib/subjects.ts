export const subjectCodes = ['MATH', 'CHINESE', 'ENGLISH', 'PHYSICS', 'CHEMISTRY'] as const

export type SubjectCode = typeof subjectCodes[number]

export const subjectNames: Record<string, string> = { MATH: '数学', CHINESE: '语文', ENGLISH: '英语', PHYSICS: '物理', CHEMISTRY: '化学' }
