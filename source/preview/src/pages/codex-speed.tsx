import {
  Alert,
  Box,
  Checkbox,
  FormControlLabel,
  LinearProgress,
  MenuItem,
  Paper,
  Select,
  Stack,
  Typography,
} from '@mui/material'
import { useEffect, useState } from 'react'

import { BasePage } from '@/components/base'
import { getCodexSpeed, type CodexGroup, type CodexView } from '@/services/codex-speed'

const count = new Intl.NumberFormat('zh-CN')
const date = (value?: string) =>
  value && !value.startsWith('0001-') ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '尚未读取'
const price = (value: number | null) => value == null ? '—' : `≈$${value.toFixed(2)}`

function UsageCard({ title, rows, model }: { title: string; rows: CodexGroup[]; model?: boolean }) {
  const max = Math.max(1, ...rows.map((row) => row.tokens))
  return (
    <Paper variant="outlined" sx={{ p: 2.5, flex: '1 1 400px', minWidth: 0 }}>
      <Typography variant="h6" sx={{ fontWeight: 700, mb: 2 }}>{title}</Typography>
      {rows.length === 0 ? <Typography color="text.secondary">该时间范围暂无用量</Typography> : (
        <Stack spacing={2.3}>
          {rows.map((row) => (
            <Box key={row.id}>
              <Box sx={{ display: 'flex', gap: 1, justifyContent: 'space-between', alignItems: 'baseline', flexWrap: 'wrap', mb: 0.7 }}>
                <Typography sx={{ fontWeight: 600, overflowWrap: 'anywhere' }}>{row.name}</Typography>
                <Typography variant="body2" color="text.secondary" sx={{ fontVariantNumeric: 'tabular-nums' }}>
                  {count.format(row.tokens)} Token · {price(row.usd)}{row.percent != null ? ` · ${row.percent.toFixed(2)}%` : ''}
                </Typography>
              </Box>
              <LinearProgress variant="determinate" value={Math.max(0, Math.min(100, model ? row.percent ?? 0 : 100 * row.tokens / max))} />
            </Box>
          ))}
        </Stack>
      )}
    </Paper>
  )
}

export default function CodexSpeedPage() {
  const [view, setView] = useState<CodexView>()
  const [error, setError] = useState('')
  const [window, setWindow] = useState('')
  const [cache, setCache] = useState(true)

  useEffect(() => {
    let active = true
    let running = false
    const refresh = async () => {
      if (running) return
      running = true
      try {
        const next = await getCodexSpeed(window, cache)
        if (active) { setView(next); setError('') }
      } catch {
        if (active) setError('用量采集暂时不可用，请稍后重试。')
      } finally {
        running = false
      }
    }
    void refresh()
    const timer = globalThis.setInterval(refresh, 4000)
    return () => { active = false; globalThis.clearInterval(timer) }
  }, [window, cache])

  return (
    <BasePage title="CodeX Speed" contentStyle={{ overflow: 'auto' }}>
      <Stack spacing={2.5} sx={{ p: 2, pb: 4 }}>
        <Box>
          <Typography variant="h5" sx={{ fontWeight: 750 }}>本机 Codex 用量</Typography>
          <Typography color="text.secondary" variant="body2" sx={{ mt: 0.5 }}>
            {view?.name || '正在读取本机日志'} · {view?.repo ? `最近同步 ${date(view.lastSync)}` : '尚未连接 A 端'}
          </Typography>
        </Box>
        {error && <Alert severity="warning">{error}</Alert>}
        {view?.status && <Alert severity="warning">{view.status}</Alert>}
        <Paper variant="outlined" sx={{ p: 2.5, display: 'flex', alignItems: 'baseline', flexWrap: 'wrap', gap: 2 }}>
          <Typography color="text.secondary">本机模型速度</Typography>
          <Typography sx={{ fontWeight: 700 }}>{view?.speed?.model || '等待模型记录'}</Typography>
          <Typography variant="h4" color="primary" sx={{ fontWeight: 750, fontVariantNumeric: 'tabular-nums' }}>
            {view?.speed?.rate == null ? '—' : view.speed.rate.toFixed(2)}
          </Typography>
          <Typography color="text.secondary">tok/s</Typography>
          <Typography variant="body2" color="text.secondary">
            {view?.speed?.rate == null ? '等待可计算的本机记录' : `${view.speed.wholeRequest ? '最近请求平均（含等待）' : '最近生成速度估算'} · ${date(view.speed.at)}`}
          </Typography>
        </Paper>
        <Box sx={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: 2 }}>
          <Select size="small" value={window || view?.window || 'all'} onChange={(event) => setWindow(event.target.value)} aria-label="重置周期">
            <MenuItem value="all">全部已读记录</MenuItem>
            {(view?.windows || []).map((item) => (
              <MenuItem value={item.id} key={item.id}>
                {item.current ? '本周期 · ' : ''}{date(item.start)} – {date(item.cutoff || item.end)}
              </MenuItem>
            ))}
          </Select>
          <FormControlLabel control={<Checkbox checked={cache} onChange={(_, checked) => setCache(checked)} />} label="含缓存 Token" />
        </Box>
        <Typography variant="body2" color="text.secondary">
          美元和百分比为参考价格与自定义预算的折算，不代表官方实际扣费或额度占比。
        </Typography>
        <Box sx={{ display: 'flex', flexWrap: 'wrap', alignItems: 'stretch', gap: 2 }}>
          <UsageCard title="项目" rows={view?.displayProjects || []} />
          <UsageCard title="模型" rows={view?.displayModels || []} model />
        </Box>
        <Typography variant="caption" color="text.secondary">本机日志 {date(view?.readAt)}</Typography>
      </Stack>
    </BasePage>
  )
}
