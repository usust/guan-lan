export const startupStages = ['检查启动条件', '准备运行镜像', '创建隔离网络', '启动系统并等待就绪', '检查访问端口', '启动完成']
export function startupStage(message: string): number | undefined {
  if (message === '检查启动条件') return 0
  if (/镜像|解包/.test(message)) return 1
  if (message.includes('创建独立网络')) return 2
  if (/创建并启动服务|的健康状态|等待系统/.test(message)) return 3
  if (message.includes('公开端口映射')) return 4
  return undefined
}
