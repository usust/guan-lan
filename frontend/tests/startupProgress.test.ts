import { expect, it } from 'vitest'
import { startupStage } from '../src/startupProgress'
it('maps real startup messages to visible stages including image recovery', () => {
  expect(startupStage('检查启动条件')).toBe(0)
  expect(startupStage('已复用本机镜像，直接创建新容器')).toBe(1)
  expect(startupStage('本机镜像缺失，正在从已验证环境包恢复')).toBe(1)
  expect(startupStage('创建独立网络并检查网络隔离')).toBe(2)
  expect(startupStage('创建并启动服务 container')).toBe(3)
  expect(startupStage('等待系统 财务系统 健康检查（已等待 10 秒，最多 185 秒）')).toBe(3)
  expect(startupStage('检查公开端口映射和 TCP 连通性')).toBe(4)
})
