import { describe, it, expect } from 'vitest'
import { questionTypeTree, typeDescendants, filterTypeTree } from '../src/questionTypes'
const items = [
 { name: '盲注', parent_name: 'SQL注入', question_count: 3, usage_count: 4 },
 { name: 'WEB', parent_name: '', question_count: 1, usage_count: 2 },
 { name: 'PWN', parent_name: '', question_count: 2, usage_count: 2 },
 { name: 'SQL注入', parent_name: 'WEB', question_count: 2, usage_count: 3 },
]
describe('hierarchical question types', () => {
 it('builds out-of-order trees and aggregates direct plus descendant counts', () => {
  const tree = questionTypeTree(items)
  const web = tree.find(n => n.name === 'WEB')!
  expect(web.total_questions).toBe(6)
  expect(web.total_usage).toBe(9)
  expect(web.children[0]?.children[0]?.name).toBe('盲注')
  expect(items[1]?.question_count).toBe(1)
 })
 it('includes all descendants when selecting a parent', () => {
  expect([...typeDescendants(items, 'WEB')].sort()).toEqual(['WEB', 'SQL注入', '盲注'].sort())
  expect([...typeDescendants(items, 'SQL注入')].sort()).toEqual(['SQL注入', '盲注'].sort())
  expect([...typeDescendants(items, '')]).toEqual([''])
 })
 it('keeps ancestor paths on search and the entire subtree on parent match', () => {
  const tree = questionTypeTree(items)
  const result = filterTypeTree(tree, '盲注')
  expect(result).toHaveLength(1)
  expect(result[0]?.name).toBe('WEB')
  expect(result[0]?.children[0]?.children[0]?.name).toBe('盲注')
  expect(filterTypeTree(tree, 'web')[0]?.total_questions).toBe(6)
  expect(filterTypeTree(tree, '不存在')).toEqual([])
 })
})
