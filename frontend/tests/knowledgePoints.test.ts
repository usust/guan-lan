import { it, expect } from 'vitest'
import { knowledgeOptions, recommendedKnowledge, type KnowledgePoint } from '../src/knowledgePoints'
const types = [{ name: 'WEB', question_count: 0, usage_count: 0 }, { name: 'API安全', parent_name: 'WEB', question_count: 0, usage_count: 0 }, { name: '逆向', question_count: 0, usage_count: 0 }]
const points: KnowledgePoint[] = [
 { name: '身份认证', owner_type: 'WEB', types: ['WEB'], question_count: 0, usage_count: 0 },
 { name: 'JWT', parent_name: '身份认证', owner_type: 'WEB', types: ['WEB'], question_count: 0, usage_count: 0 },
 { name: '签名校验', parent_name: 'JWT', owner_type: 'WEB', types: ['WEB'], question_count: 0, usage_count: 0 },
 { name: '栈溢出', types: [], question_count: 0, usage_count: 0 },
]
it('recommends knowledge owned by the current type or its ancestors', () => {
 expect(recommendedKnowledge(points, types, 'API安全').map(p => p.name)).toEqual(['身份认证', 'JWT', '签名校验'])
 expect(recommendedKnowledge(points, types, '逆向')).toEqual([])
})
it('preserves selected cross-type and legacy points with ancestor paths', () => {
 const options = knowledgeOptions(points, types, 'API安全', ['栈溢出', '旧标签'], false)
 expect(options.map(p => p.name)).toEqual(['身份认证', '栈溢出', '旧标签'])
 expect(options[0]?.children[0]?.children[0]?.name).toBe('签名校验')
 expect(knowledgeOptions(points, types, 'WEB', [], true).map(p => p.name)).toEqual(['身份认证', '栈溢出'])
})
it('does not narrow available points when switching to all', () => {
 expect(recommendedKnowledge(points, types, 'PWN')).toEqual([])
 expect(knowledgeOptions(points, types, 'PWN', [], true)).toHaveLength(2)
})

it('does not guess ownership from historical multiple type associations', () => {
 const pending: KnowledgePoint[] = [{ name: '旧跨方向', types: ['WEB', '逆向'], owner_type: '', question_count: 0, usage_count: 0 }]
 expect(recommendedKnowledge(pending, types, 'WEB')).toEqual([])
 expect(knowledgeOptions(pending, types, 'WEB', ['旧跨方向'], false)[0]?.name).toBe('旧跨方向')
})
