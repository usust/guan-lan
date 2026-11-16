import { expect, it } from 'vitest'
import { finishChecklist, updateChecklist, verificationChecklist } from '../src/verificationChecklist'
import type { EnvironmentManifest } from '../src/types'
const environment = { images: [{ key: 'web-image' }], services: [{ key: 'web' }] } as EnvironmentManifest
it('shows every check as pending before a manual run', () => {
  const items = verificationChecklist(environment)
  expect(items.every(item => item.state === 'pending')).toBe(true)
  expect(items.some(item => item.label.includes('web-image'))).toBe(true)
  expect(items.some(item => item.label.includes('web 健康'))).toBe(true)
})
it('updates the actual current check and completes a successful run', () => {
  const items = verificationChecklist(environment)
  updateChecklist(items, '检查 Docker Engine')
  expect(items[0]!.state).toBe('running')
  updateChecklist(items, '校验环境包并导入镜像')
  expect(items[0]!.state).toBe('passed')
  expect(items[1]!.state).toBe('running')
  finishChecklist(items, true)
  expect(items.every(item => item.state === 'passed')).toBe(true)
})
it('keeps a failed service distinct from successful cleanup and unexecuted checks', () => {
  const items = verificationChecklist(environment)
  updateChecklist(items, '创建并启动服务 web')
  updateChecklist(items, '试运行失败：启动后退出')
  updateChecklist(items, '清理验收容器和网络')
  updateChecklist(items, '测试容器和网络清理完成')
  finishChecklist(items, false)
  expect(items.find(item => item.label === '服务 web 启动')!.state).toBe('failed')
  expect(items.find(item => item.label === '测试容器和网络清理')!.state).toBe('passed')
  expect(items.at(-1)!.state).toBe('skipped')
})
it('shows system names while preserving internal progress triggers', () => {
  const items = verificationChecklist({ images: [{ key: 'image', source_file: 'finance.tar' }], services: [{ key: 'container', name: '财务系统' }] } as EnvironmentManifest)
  expect(items.some(item => item.label.includes('finance.tar'))).toBe(true)
  updateChecklist(items, '检查服务 container 的健康状态和安全策略')
  expect(items.find(item => item.state === 'running')?.label).toBe('服务 财务系统 健康与安全策略')
})
it('does not mark a service as failed when only the connection was interrupted', () => {
  const items = verificationChecklist(environment)
  updateChecklist(items, '检查服务 web 的健康状态和安全策略')
  finishChecklist(items, false, true)
  expect(items.find(item => item.trigger === '检查服务 web 的健康状态和安全策略')?.state).toBe('interrupted')
  expect(items.some(item => item.state === 'failed')).toBe(false)
})
