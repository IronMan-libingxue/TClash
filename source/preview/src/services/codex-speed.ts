import { invoke } from '@tauri-apps/api/core'

export interface CodexGroup {
  id: string
  name: string
  tokens: number
  usd: number | null
  percent?: number | null
  unpriced?: number
  lastAt?: string
}

export interface CodexActivity {
  at: string
  active: number
  fast: number
  uncertain: boolean
  fastUncertain: boolean
  incomplete: boolean
}

export interface CodexTraffic {
  month: string
  since: string
  at: string
  upload: number
  download: number
  incomplete: boolean
  groups?: { name: string; scope?: string; upload: number; download: number }[]
}

export function trafficGroupName(group: { name: string; scope?: string }): string {
  if (!group.scope || group.scope === 'legacy') return `${group.name}（早期采集，配置未区分）`
  if (group.scope === 'unknown') return `${group.name}（配置未知）`
  return `${group.name}（配置 ${group.scope.slice(0, 6)}）`
}

export interface CodexView {
  name: string
  repo?: string
  status?: string
  readAt: string
  lastSync: string
  countSince?: string
  window: string
  windows?: { id: string; start: string; end: string; cutoff?: string; current: boolean }[]
  speed: { model: string; rate?: number; at: string; wholeRequest?: boolean; state?: 'active' | 'idle' | 'unknown' }
  displayProjects?: CodexGroup[]
  displayModels?: CodexGroup[]
  telemetry?: { version: number; activity?: CodexActivity; traffic?: CodexTraffic }
  activityStats?: { occurrences: number; seconds: number; peak: number; incomplete: boolean }
}

export async function getCodexSpeed(window: string, cache: boolean): Promise<CodexView> {
  const response = await invoke<string>('get_codex_speed', { window, cache })
  return JSON.parse(response) as CodexView
}

export function freshActivity(view?: CodexView): CodexActivity | undefined {
  const activity = view?.telemetry?.activity
  if (!activity) return undefined
  const age = Date.now() - Date.parse(activity.at)
  return Number.isFinite(age) && age >= -300000 && age < 30000 ? activity : undefined
}

export function trafficBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) { value /= 1024; unit += 1 }
  return `${value.toFixed(unit ? 2 : 0)} ${units[unit]}`
}
