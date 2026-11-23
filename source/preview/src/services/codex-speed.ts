import { invoke } from '@tauri-apps/api/core'

export interface CodexGroup {
  id: string
  name: string
  tokens: number
  usd: number | null
  percent?: number | null
  unpriced?: number
}

export interface CodexView {
  name: string
  repo?: string
  status?: string
  readAt: string
  lastSync: string
  window: string
  windows?: { id: string; start: string; end: string; cutoff?: string; current: boolean }[]
  speed: { model: string; rate?: number; at: string; wholeRequest?: boolean }
  displayProjects?: CodexGroup[]
  displayModels?: CodexGroup[]
}

export async function getCodexSpeed(window: string, cache: boolean): Promise<CodexView> {
  const response = await invoke<string>('get_codex_speed', { window, cache })
  return JSON.parse(response) as CodexView
}
