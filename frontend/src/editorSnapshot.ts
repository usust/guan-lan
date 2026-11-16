// Copy the JSON model without serializing potentially large Base64 strings.
export function copyEditorValue<T>(value: T): T {
  if (Array.isArray(value)) return value.map(copyEditorValue) as T
  if (value !== null && typeof value === 'object') {
    return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, copyEditorValue(item)])) as T
  }
  return value
}
export function equalEditorValue(a: unknown, b: unknown): boolean {
  if (a === b) return true
  if (a === null || b === null || typeof a !== 'object' || typeof b !== 'object') return false
  if (Array.isArray(a) !== Array.isArray(b)) return false
  const keys = Object.keys(a)
  if (keys.length !== Object.keys(b).length) return false
  return keys.every(key => Object.hasOwn(b, key) && equalEditorValue(
    (a as Record<string, unknown>)[key], (b as Record<string, unknown>)[key],
  ))
}
