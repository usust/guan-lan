export interface Attachment { name: string; data: string; file_id?: string; size?: number }
export type Role = 'ADMIN' | 'PLAYER'
export type UserStatus = 'ACTIVE' | 'DISABLED'

export interface User {
  id: string | number
  username: string
  display_name: string
  role: Role
  status: UserStatus
}

export interface Session {
  user: User
  csrf_token: string
}

export interface SystemInfo {
  platform_id: string
  root: string
  os: string
  arch: string
  resources: { available: boolean; containers: number; running: number; memory_used: number; memory_total: number; cpus?: number; error?: string }
  disk: { available: boolean; used: number; total: number }
  docker: {
    available: boolean
    endpoint: string
    version?: string | null
    os?: string | null
    architecture?: string | null
    error?: string | null
  }
}

export interface CreateUserInput {
  username: string
  display_name: string
  password: string
  role: Role
}

export interface EnvironmentManifest {
  format_version: number
  version: string
  architecture: 'amd64' | 'arm64'
  images: { source_file?: string; name?: string; key: string; file: string; sha256: string }[]
  services: {
    id?: string; name?: string; key: string; image: string; depends_on: string[] | null; memory_mb: number; cpu: number; resource_limits_enabled?: boolean; pids_limit?: number
    ports: { port: number; protocol: 'tcp' | 'udp'; public: boolean; label: string }[] | null
  }[]
}
export interface Subtask {
  attachments?: Attachment[]
  service_keys?: string[]
  id: string
  title: string
  content: string
  points: number
  service_key: string
  flag?: string
  flag_configured: boolean
  solved: boolean
  attempts: number
}
export interface Challenge {
  solution_document?: { name: string; data: string } | null
  attachments?: Attachment[]
  source_id?: string
  id: string
  title: string
  description: string
  tags?: string[]
  category: string
  difficulty: string
  environment: EnvironmentManifest | null
  package_validated: boolean
  package_token?: string
  package_sha256?: string
	 runtime_ready?: boolean
	 has_environment?: boolean
  subtasks: Subtask[]
}
export interface ChallengeSet {
  library?: boolean
  id: string
  title: string
  description: string
  status: 'DRAFT' | 'PUBLISHED' | 'CLOSED'
  starts_at: number
  ends_at: number
  version: number
  questions: Challenge[]
}
export interface SetResponse {
  set: ChallengeSet
  runtime_available?: boolean
  runtime_message?: string
}
export interface RuntimeInstance {
  id?: string
  state: 'STOPPED' | 'STARTING' | 'RUNNING' | 'FAILED'
  expires_at?: number
  endpoints: { service: string; port: number; protocol: string; address: string }[]
}
export interface Ranking {
  user_id: string
  display_name: string
  points: number
  solved: number
  last_solve: number
}
export interface Submission {
  id: string
  subtask_id: string
  title: string
  correct: boolean
  created_at: number
}
