import { useEffect, useState } from 'react'

import { getCodexSpeed, type CodexView } from '@/services/codex-speed'

type Filters = { window: string; cache: boolean }
let filters: Filters = { window: '', cache: true }
const listeners = new Set<() => void>()

export function setCodexFilters(next: Partial<Filters>) {
  filters = { ...filters, ...next }
  listeners.forEach((listener) => listener())
}

export function useCodexView() {
  const [view, setView] = useState<CodexView>()
  const [error, setError] = useState(false)
  const [selection, setSelection] = useState(filters)

  useEffect(() => {
    let active = true
    let running = false
    let requested = 0
    const refresh = async () => {
      if (running) { requested += 1; return }
      running = true
      const version = requested
      const selected = filters
      setSelection(selected)
      try {
        const next = await getCodexSpeed(selected.window, selected.cache)
        if (active && version === requested) { setView(next); setError(false) }
      } catch {
        if (active && version === requested) setError(true)
      } finally {
        running = false
        if (active && version !== requested) void refresh()
      }
    }
    listeners.add(refresh)
    void refresh()
    const timer = globalThis.setInterval(refresh, 4000)
    return () => { active = false; listeners.delete(refresh); globalThis.clearInterval(timer) }
  }, [])

  return { view, error, filters: selection }
}
