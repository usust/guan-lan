import type { EnvironmentManifest } from './types'
export type CheckState = 'pending' | 'running' | 'passed' | 'failed' | 'skipped' | 'interrupted'
export interface CheckItem { label: string; trigger: string; state: CheckState }
export function verificationChecklist(environment: EnvironmentManifest): CheckItem[] {
  const labels = [
    ['Docker Engine 连接', '检查 Docker Engine'],
    ['环境包完整性', '校验环境包并导入镜像'],
    ...environment.images.map(image => [`上传文件 ${image.source_file || image.name || image.key} 导入与架构`, `解包并导入镜像 ${image.key}`]),
    ['独立网络与隔离', '创建独立网络并检查网络隔离'],
    ...environment.services.flatMap(service => [
      [`服务 ${service.name || service.key} 启动`, `创建并启动服务 ${service.key}`],
      [`服务 ${service.name || service.key} 健康与安全策略`, `检查服务 ${service.key} 的健康状态和安全策略`],
    ]),
    ['公开端口映射与 TCP 连通性', '检查公开端口映射和 TCP 连通性'],
    ['测试容器和网络清理', '清理验收容器和网络'],
    ['保存验收结果', '保存验收结果'],
  ]
  return labels.map(([label, trigger]) => ({ label: label!, trigger: trigger!, state: 'pending' }))
}
export function updateChecklist(items: CheckItem[], message: string) {
  if (message.startsWith('试运行失败：')) {
    items.filter(item => item.state === 'running').forEach(item => { item.state = 'failed' })
    return
  }
  if (message === '测试容器和网络清理完成') {
    const cleanup = items.find(item => item.trigger === '清理验收容器和网络')
    if (cleanup) cleanup.state = 'passed'
    return
  }
  const next = items.find(item => item.trigger === message)
  if (!next) return
  items.filter(item => item.state === 'running').forEach(item => { item.state = 'passed' })
  next.state = 'running'
}
export function finishChecklist(items: CheckItem[], success: boolean, interrupted = false) {
  items.forEach(item => {
    if (item.state === 'running') item.state = success ? 'passed' : interrupted ? 'interrupted' : 'failed'
    else if (item.state === 'pending') item.state = success ? 'passed' : 'skipped'
  })
}
