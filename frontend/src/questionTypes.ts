export interface QuestionTypeEntry {
 name: string
 parent_name?: string
 question_count: number
 usage_count: number
 knowledge_count?: number
}
export interface QuestionTypeNode extends QuestionTypeEntry {
 children: QuestionTypeNode[]
 total_questions: number
 total_usage: number
}
export function questionTypeTree(items: QuestionTypeEntry[]): QuestionTypeNode[] {
 const nodes = new Map(items.map(item => [item.name, { ...item, children: [], total_questions: item.question_count, total_usage: item.usage_count } as QuestionTypeNode]))
 const roots: QuestionTypeNode[] = []
 for (const node of nodes.values()) {
  const parent = node.parent_name && nodes.get(node.parent_name)
  if (parent) parent.children.push(node)
  else roots.push(node)
 }
 function sum(node: QuestionTypeNode) {
  node.children.forEach(child => { sum(child); node.total_questions += child.total_questions; node.total_usage += child.total_usage })
 }
 roots.forEach(sum)
 return roots
}
export function typeDescendants(items: QuestionTypeEntry[], name: string): Set<string> {
 const result = new Set([name])
 let added = true
 while (added) {
  added = false
  for (const item of items) if (item.parent_name && result.has(item.parent_name) && !result.has(item.name)) { result.add(item.name); added = true }
 }
 return result
}
export function filterTypeTree(nodes: QuestionTypeNode[], keyword: string): QuestionTypeNode[] {
 const query = keyword.trim().toLowerCase()
 if (!query) return nodes
 return nodes.flatMap(node => {
  if (node.name.toLowerCase().includes(query)) return [node]
  const children = filterTypeTree(node.children, keyword)
  return children.length ? [{ ...node, children }] : []
 })
}
