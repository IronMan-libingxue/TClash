import type { CheckOptions, Update } from '@tauri-apps/plugin-updater'

export const checkUpdateSafe = async (_options?: CheckOptions): Promise<Update | null> => {
  throw new Error('TClash Preview 当前使用手动安装更新')
}

export type { CheckOptions }
