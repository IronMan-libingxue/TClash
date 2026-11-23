import { invoke } from '@tauri-apps/api/core'

export const exportTClashProfile = (uid: string, destination: string) =>
  invoke<void>('export_tclash_profile', { uid, destination })

export const importTClashProfile = (source: string) =>
  invoke<void>('import_tclash_profile', { source })
