import { describe, expect, it } from 'vitest'
import { readVerificationStream } from '../src/runtimeVerification'

function response(parts: string[]) {
  return new Response(new ReadableStream({ start(controller) { for (const part of parts) controller.enqueue(new TextEncoder().encode(part)); controller.close() } }))
}
describe('runtime verification progress', () => {
  it('reads split progress records and completion', async () => {
    const messages: string[] = []
    await readVerificationStream(response(['{"type":"progress","message":"导入', '镜像"}\n{"type":"done","message":"通过"}\n']), event => messages.push(event.message))
    expect(messages).toEqual(['导入镜像', '通过'])
  })
  it('preserves and rejects a backend failure', async () => {
    const messages: string[] = []
    await expect(readVerificationStream(response(['{"type":"error","message":"端口超时"}\n']), event => messages.push(event.message))).rejects.toThrow('端口超时')
    expect(messages).toEqual(['端口超时'])
  })
  it('rejects a connection closed without a result', async () => {
    await expect(readVerificationStream(response(['{"type":"progress","message":"启动"}\n']), () => {})).rejects.toThrow('验收连接中断')
  })
})

it('consumes heartbeats without changing the visible verification step', async () => {
  const events: string[] = []
  let activity = 0
  await readVerificationStream(response(['{"type":"heartbeat","message":"alive"}\n', '{"type":"done","message":"ok"}\n']), event => events.push(event.type), () => { activity++ })
  expect(events).toEqual(['done'])
  expect(activity).toBeGreaterThan(0)
})
