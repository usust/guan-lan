import { reactive } from 'vue'
import { request } from '../api'

export interface PlatformBranding { name: string; description: string; icon: string }
export interface PlatformSettings {
  branding: PlatformBranding
  root: string
  platform_id: string
  paths: Record<string, string>
  config_file: string
  listen: string
  allowed_origin: string
  secure_cookie: boolean
  docker_endpoint: string
  runtime_public_host: string
  min_free_bytes: number
}
export const platform = reactive<PlatformBranding>({ name: '华山平台', description: '竞赛与训练', icon: '' })
export async function loadPlatform(): Promise<void> {
  Object.assign(platform, await request<PlatformBranding>('/platform'))
}
