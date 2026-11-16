import { type QuestionTypeEntry, questionTypeTree } from './questionTypes'
export interface KnowledgePoint extends QuestionTypeEntry { types: string[]; owner_type?: string }
export function recommendedKnowledge(points: KnowledgePoint[], types: QuestionTypeEntry[], category: string): KnowledgePoint[] {
 const ancestors = new Set<string>()
 let current = category
 while (current && !ancestors.has(current)) { ancestors.add(current); current = types.find(t => t.name === current)?.parent_name ?? '' }
 return points.filter(point => !!point.owner_type && ancestors.has(point.owner_type))
}
// Keep ancestors and already-selected values visible without changing selection.
export function knowledgeOptions(points: KnowledgePoint[], types: QuestionTypeEntry[], category: string, selected: string[], all: boolean) {
 const names = new Set((all ? points : recommendedKnowledge(points, types, category)).map(p => p.name))
 selected.forEach(name => names.add(name))
 for (const point of points) if (names.has(point.name)) {
  let parent = point.parent_name
  const seen = new Set<string>()
  while (parent && !seen.has(parent)) { seen.add(parent); names.add(parent); parent = points.find(p => p.name === parent)?.parent_name }
 }
 const available = points.filter(p => names.has(p.name))
 for (const name of selected) if (!available.some(p => p.name === name)) available.push({ name, types: [], question_count: 0, usage_count: 0 })
 return questionTypeTree(available)
}
