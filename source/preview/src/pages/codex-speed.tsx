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
import { BasePage } from '@/components/base'
import { setCodexFilters, useCodexView } from '@/hooks/use-codex-view'
import { freshActivity, trafficBytes, trafficGroupName, type CodexGroup } from '@/services/codex-speed'

const count = new Intl.NumberFormat('zh-CN')
const date = (value?: string) =>
  value && !value.startsWith('0001-') ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '尚未读取'
const beijingDate = (value?: string) =>
  value && !value.startsWith('0001-') && Number.isFinite(Date.parse(value))
    ? new Date(value).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai', hour12: false })
    : '未知时间'
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
                  {count.format(row.tokens)} Token · {price(row.usd)}{row.percent != null ? ` · ${row.percent.toFixed(2)}%` : ''}{row.unpriced ? ` · ${count.format(row.unpriced)} 条未计价` : ''}
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
  const { view, error, filters } = useCodexView()
  const activity = freshActivity(view)
  const concurrent = (activity?.active ?? 0) >= 2
  const traffic = view?.telemetry?.traffic

  return (
    <BasePage title="CodeX Speed" contentStyle={{ overflow: 'auto' }}>
      <Stack spacing={2.5} sx={{ p: 2, pb: 4 }}>
        <Box>
          <Typography variant="h5" sx={{ fontWeight: 750 }}>本机 Codex 用量</Typography>
        </Box>
        {error && <Alert severity="warning">用量采集暂时不可用，请稍后重试。</Alert>}
        {view?.status && <Alert severity="warning">{view.status}</Alert>}
        <Paper variant="outlined" sx={(theme) => ({ p: 2.5, display: 'flex', alignItems: 'baseline', flexWrap: 'wrap', gap: 2,
          ...(concurrent ? { borderColor: theme.palette.warning.light, backgroundColor: theme.palette.mode === 'dark' ? '#302d25' : '#fffaf0' } : {}) })}>
          <Typography color="text.secondary">本机模型速度</Typography>
          <Typography sx={{ fontWeight: 700 }}>{view?.speed?.model || '等待模型记录'}</Typography>
          {!!activity?.fast && <Typography color="primary">⚡ 有会话选择 Fast</Typography>}
          <Typography variant="h4" color="primary" sx={{ fontWeight: 750, fontVariantNumeric: 'tabular-nums' }}>
            {view?.speed?.rate == null ? '—' : view.speed.rate.toFixed(2)}
          </Typography>
          <Typography color="text.secondary">tok/s</Typography>
          <Typography variant="body2" color="text.secondary">
            {view?.speed?.state === 'idle' ? '当前没有运行中的会话' : view?.speed?.state === 'unknown' ? '当前会话状态未知' : view?.speed?.rate == null ? '等待可计算的本机记录' : `${view.speed.wholeRequest ? '最近请求平均（含等待）' : '最近生成速度估算'} · ${date(view.speed.at)}`}
          </Typography>
        </Paper>
        {concurrent && <Alert severity="warning">当前存在高并发风险，请仅保留一个任务运行。</Alert>}
        <Typography variant="body2" color="text.secondary">
          {activity ? `运行中的独立会话：${activity.active}${activity.uncertain ? '（另有状态未知）' : ''}${activity.fastUncertain ? ' · 部分 Fast 状态未知' : ''}` : '会话状态尚未确认'}
          {view?.activityStats && ` · 本周期并发 ${view.activityStats.occurrences} 次，${Math.floor(view.activityStats.seconds / 60)} 分钟，峰值 ${view.activityStats.peak} 个${view.activityStats.incomplete ? '（记录不完整）' : ''}`}
        </Typography>
        <Box sx={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: 2 }}>
          <Select size="small" value={filters.window || view?.window || 'all'} onChange={(event) => setCodexFilters({ window: event.target.value })} aria-label="重置周期">
            <MenuItem value="all">全部已读记录</MenuItem>
            {(view?.windows || []).map((item) => (
              <MenuItem value={item.id} key={item.id}>
                {item.current ? '本周期 · ' : ''}{date(item.start)} – {date(item.cutoff || item.end)}
              </MenuItem>
            ))}
          </Select>
          <FormControlLabel control={<Checkbox checked={filters.cache} onChange={(_, checked) => setCodexFilters({ cache: checked })} />} label="含缓存 Token" />
        </Box>
        <Box sx={{ display: 'flex', flexWrap: 'wrap', alignItems: 'stretch', gap: 2 }}>
          <UsageCard title="项目" rows={view?.displayProjects || []} />
          <UsageCard title="模型" rows={view?.displayModels || []} model />
        </Box>
        <Paper variant="outlined" sx={{ p: 2.5 }}>
          <Typography variant="h6" sx={{ fontWeight: 700, mb: 1 }}>代理组当月流量</Typography>
          {traffic ? <>
            <Typography variant="body2" color="text.secondary">
              {traffic.month}（北京时间） · 组总计 {trafficBytes(traffic.upload + traffic.download)} · 上传 {trafficBytes(traffic.upload)} · 下载 {trafficBytes(traffic.download)}
            </Typography>
            {(traffic.groups || []).map((group) => <Typography key={`${group.scope || ''}:${group.name}`} variant="body2" sx={{ mt: 1, overflowWrap: 'anywhere' }}>
              {trafficGroupName(group)} · 上传 {trafficBytes(group.upload)} · 下载 {trafficBytes(group.download)} · 合计 {trafficBytes(group.upload + group.download)}
            </Typography>)}
            <Typography variant="caption" color="text.secondary">
              从 {beijingDate(traffic.since)}（北京时间）开始采集 · 数据 {date(traffic.at)}
            </Typography>
          </> : <Typography variant="body2" color="text.secondary">尚未获取 · 快照统计，可能漏计</Typography>}
        </Paper>
        <Typography variant="caption" color="text.secondary">本机日志 {date(view?.readAt)}{view?.countSince && !view.countSince.startsWith('0001-') ? ` · 统计起点 ${date(view.countSince)}` : ''}</Typography>
      </Stack>
    </BasePage>
  )
}
