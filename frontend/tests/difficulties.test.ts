import { afterEach, expect, it } from 'vitest'
import { difficulties, difficultyLevels, setDifficulties } from '../src/stores/difficulties'

afterEach(() => setDifficulties({ EASY: '简单', MEDIUM: '中等', HARD: '困难' }))

it('updates available choices after adding, renaming and deleting difficulties', () => {
 setDifficulties({ EASY: '简单', CUSTOM_TEST: '专家' })
 expect(difficultyLevels.value).toEqual(['EASY', 'CUSTOM_TEST'])
 expect(difficulties.MEDIUM).toBeUndefined()
 setDifficulties({ CUSTOM_TEST: '大师' })
 expect(difficultyLevels.value).toEqual(['CUSTOM_TEST'])
 expect(difficulties.CUSTOM_TEST).toBe('大师')
 expect(difficulties.EASY).toBeUndefined()
})
