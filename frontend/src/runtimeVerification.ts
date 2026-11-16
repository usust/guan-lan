export class VerificationConnectionError extends Error {}

export interface VerificationEvent { type: 'progress' | 'done' | 'error' | 'heartbeat'; message: string; data?: unknown }

export async function readVerificationStream(response: Response, report: (event: VerificationEvent) => void, activity: () => void = () => {}) {
  if (!response.body) throw new Error('服务器未返回验收进度')
  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  let finished = false
  function line(value: string) {
    if (!value.trim()) return
    const event = JSON.parse(value) as VerificationEvent
    if (!['progress', 'done', 'error', 'heartbeat'].includes(event.type) || typeof event.message !== 'string') throw new Error('验收进度格式无效')
    if (event.type === 'heartbeat') return
    report(event)
    if (event.type === 'error') throw new Error(event.message)
    if (event.type === 'done') finished = true
  }
  try {
    while (!finished) {
      const { value, done } = await reader.read().catch(() => { throw new VerificationConnectionError('验收连接中断，请刷新状态后重试') })
      activity()
      buffer += decoder.decode(value, { stream: !done })
      let newline: number
      while ((newline = buffer.indexOf('\n')) >= 0) {
        line(buffer.slice(0, newline)); buffer = buffer.slice(newline + 1)
      }
      if (done) { line(buffer); break }
    }
    if (!finished) throw new VerificationConnectionError('验收连接中断，请刷新状态后重试')
  } finally { await reader.cancel().catch(() => {}); reader.releaseLock() }
}
