import { computed, reactive } from 'vue'
import { request } from '../api'

export type DifficultySettings = Record<string, string>
export const difficulties = reactive<DifficultySettings>({ EASY: '简单', MEDIUM: '中等', HARD: '困难' })
export const difficultyLevels = computed(() => {
 const defaults: Record<string, number> = { EASY: 0, MEDIUM: 1, HARD: 2 }
 return Object.keys(difficulties).sort((a, b) => (defaults[a] ?? 3) - (defaults[b] ?? 3) || a.localeCompare(b))
})
export function setDifficulties(settings: DifficultySettings): void {
 for (const code of Object.keys(difficulties)) delete difficulties[code]
 Object.assign(difficulties, settings)
}
export async function loadDifficulties(): Promise<void> {
 setDifficulties(await request<DifficultySettings>('/difficulties'))
}
