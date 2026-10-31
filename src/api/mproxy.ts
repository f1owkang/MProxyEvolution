/**
 * MProxyEvolution `/api` client.
 *
 * The zashboard bundle is served by the MProxyEvolution Go backend on the same origin.
 * `public/mproxy-boot.js` stores the hand-off token in
 * `sessionStorage['mproxy/token']` and `window.__MPROXY_TOKEN__`; every call
 * sends it as `X-MProxy-Token` (the backend also accepts `Authorization`).
 */
export type ApplyMode = 'auto' | 'reload' | 'restart' | 'none'
export type ProxyMode = 'tun' | 'mixed'
export type UpdateChannel = 'stable' | 'alpha'
export type AppsMode = 'all' | 'whitelist' | 'blacklist'
export type ImportMode = 'merge' | 'replace'
export type JobState = 'running' | 'done' | 'error'

declare global {
  interface Window {
    __MPROXY_TOKEN__?: string
  }
}

const TOKEN_STORAGE_KEY = 'mproxy/token'
const REQUEST_TIMEOUT_MS = 30_000

export class MProxyApiError extends Error {
  readonly status: number
  readonly code: string
  readonly details: unknown

  constructor(
    message: string,
    options: { status?: number; code?: string; details?: unknown } = {},
  ) {
    super(message)
    this.name = 'MProxyApiError'
    this.status = options.status ?? 0
    this.code = options.code ?? ''
    this.details = options.details
  }
}

export const getMProxyToken = (): string => {
  const fromWindow = window.__MPROXY_TOKEN__
  if (fromWindow) return fromWindow

  try {
    return window.sessionStorage.getItem(TOKEN_STORAGE_KEY) ?? ''
  } catch {
    return ''
  }
}

export const hasMProxyToken = (): boolean => getMProxyToken().length > 0

export type HealthResponse = {
  ok: boolean
  version: string
  pid: number
}

export type CoreControllerStatus = {
  host: string
  port: number
  reachable: boolean
  secretSet: boolean
  mode: string
  mixedPort: number
  socksPort: number
  httpPort: number
  tunEnabled: boolean
}

export type CoreStatus = {
  installed: boolean
  running: boolean
  pid: number
  version: string
  startedAt: string
  uptimeSec: number
  /** 后端看门狗累计自动重启次数（后端新增字段，旧后端可能缺失）。 */
  restarts?: number
  /** 最近一次内核退出摘要（time/pid/err，后端新增字段）。 */
  lastExit?: string
  controller: CoreControllerStatus
}

export type ConfigSummary = {
  mode: string
  logLevel: string
  mixedPort: number
  socksPort: number
  port: number
  allowLan: boolean
  ipv6: boolean
  tunEnabled: boolean
  tunStack: string
  proxyMode: ProxyMode
  autoStart: boolean
  proxyCount: number
  groupCount: number
  ruleCount: number
  providerCount: number
}

export type ConfigStatus = {
  path: string
  exists: boolean
  size: number
  modifiedAt: string
  valid: boolean
  summary: ConfigSummary
}

export type SystemStatus = {
  arch: string
  abi: string
  android: string
  model: string
  kernel: string
  memTotal: number
  memFree: number
  load1: number
  uptimeSec: number
}

export type UpdateStatus = {
  channel: UpdateChannel
  ghproxy: string
}

export type BackendStatus = {
  version: string
  pid: number
  uptimeSec: number
  startedAt: string
  dataDir: string
  moduleDir: string
}

export type MProxyStatus = {
  backend: BackendStatus
  core: CoreStatus
  config: ConfigStatus
  system: SystemStatus
  update: UpdateStatus
}

export type MetaResponse = {
  url: string
  version: string
}

export type ConfigResponse = {
  yaml: string
  summary: ConfigSummary
  path: string
  modifiedAt: string
  valid: boolean
  validationOutput: string
}

export type ValidateConfigResponse = {
  ok: boolean
  output: string
}

export type SaveConfigResponse = {
  ok: boolean
  saved: boolean
  applied: boolean
  restarted: boolean
  summary?: ConfigSummary
  validationOutput?: string
}

export type ImportedConfigInfo = {
  proxies: number
  groups: number
  rules: number
  providers: number
  kind: string
}

export type ImportConfigResponse = {
  ok: boolean
  saved: boolean
  applied: boolean
  restarted: boolean
  imported: ImportedConfigInfo
  warnings: string[]
  summary?: ConfigSummary
}

export type CoreUpdateCheckResponse = {
  current: string
  latest: string
  available: boolean
  channel: UpdateChannel
  abi: string
}

export type JobResponse<T = unknown> = {
  id: string
  type: string
  state: JobState
  progress: number
  message: string
  error: string
  startedAt: string
  finishedAt: string
  result?: T
}

export type LogLinesResponse = {
  lines?: string | string[]
  log?: string
  content?: string
  total?: number
}

export type PackageInfo = {
  packageName: string
  uid: number
  system: boolean
}

export type PackagesResponse = {
  packages: PackageInfo[]
}

export type MProxySettings = {
  x: {
    autoStart: boolean
    proxyMode: ProxyMode
    updateChannel: UpdateChannel
    ghproxy: string
    onboarded: boolean
    /** Private DNS 策略：auto=TUN 运行时临时关闭；keep=不修改系统设置（旧后端可能缺失）。 */
    privateDNS?: 'auto' | 'keep'
  }
  coreVersion: string
  abi: string
  installed: boolean
  running: boolean
  canUpdate: boolean
}

export type MProxySettingsPatch = Partial<MProxySettings['x']>

// PATCH /api/mproxy/settings 返回 { ok, changed, settings }；settings 是扁平的
// x-mproxy 设置值（不是 GET 的 { x: ... } 包装）。
export type MProxySettingsPatchResponse = {
  ok: boolean
  changed: boolean
  applied?: boolean
  restarted?: boolean
  warnings?: string[]
  settings: MProxySettings['x']
}

export type MProxyAppsResponse = {
  mode: AppsMode
  apps: string[]
  tunEnabled: boolean
}

// PUT /api/mproxy/apps 返回操作结果，不包含 tunEnabled（如需请重新 GET）。
export type MProxyAppsPutResponse = {
  ok: boolean
  applied: boolean
  restarted: boolean
  warnings?: string[]
  mode: AppsMode
  apps: string[]
}

export type MProxyProviderInput = {
  name: string
  url: string
  interval?: number
  path?: string
}

export type MProxyProviderInfo = {
  name: string
  type?: string
  url?: string
  path?: string
  interval?: number
}

// POST/DELETE /api/mproxy/providers 返回操作结果与当前 provider 列表。
export type MProxyProviderResponse = {
  ok: boolean
  applied?: boolean
  restarted?: boolean
  warnings?: string[]
  providers: MProxyProviderInfo[]
}

export type DiagnosticCheck = {
  name: string
  ok: boolean
  detail: string
}

export type DiagnosticFile = {
  name: string
  path: string
  exists: boolean
  size: number
  modifiedAt?: string
}

export type DiagnosticsResponse = {
  generatedAt?: string
  version?: string
  dataDir?: string
  moduleDir?: string
  checks?: DiagnosticCheck[]
  files?: DiagnosticFile[]
  logTail?: string[]
  [key: string]: unknown
}

export type MihomoProvider = {
  name: string
  vehicleType?: string
  type?: string
  updatedAt?: string
  subscriptionInfo?: {
    Download?: number
    Upload?: number
    Total?: number
    Expire?: number
  }
  proxies?: unknown[]
}

export type MihomoProvidersResponse = {
  providers?: Record<string, MihomoProvider>
}

type QueryValue = string | number | boolean | undefined

type RequestOptions = {
  method?: string
  body?: unknown
  query?: Record<string, QueryValue>
  timeoutMs?: number
}

const buildQuery = (query?: Record<string, QueryValue>): string => {
  if (!query) return ''

  const params = new URLSearchParams()
  Object.entries(query).forEach(([key, value]) => {
    if (value === undefined || value === '') return
    params.set(key, String(value))
  })

  const encoded = params.toString()
  return encoded ? `?${encoded}` : ''
}

const parseError = async (response: Response): Promise<MProxyApiError> => {
  let message = `${response.status} ${response.statusText}`
  let code = ''
  let details: unknown

  try {
    const body = (await response.json()) as {
      error?: { code?: string; message?: string; details?: unknown }
    }
    if (body?.error) {
      message = body.error.message || message
      code = body.error.code || ''
      details = body.error.details
    }
  } catch {
    // ignore non-JSON error bodies
  }

  return new MProxyApiError(message, { status: response.status, code, details })
}

const fetchWithTimeout = async (url: string, init: RequestInit, timeoutMs: number) => {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), timeoutMs)

  try {
    return await fetch(url, { ...init, signal: controller.signal, cache: 'no-store' })
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') {
      throw new MProxyApiError('请求超时', { code: 'timeout' })
    }
    throw new MProxyApiError(error instanceof Error ? error.message : '网络错误', {
      code: 'network',
    })
  } finally {
    clearTimeout(timer)
  }
}

const authHeaders = (): Record<string, string> => {
  const token = getMProxyToken()
  return token ? { 'X-MProxy-Token': token } : {}
}

async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = {
    Accept: 'application/json',
    ...authHeaders(),
  }

  let body: BodyInit | undefined
  if (options.body !== undefined) {
    headers['Content-Type'] = 'application/json'
    body = JSON.stringify(options.body)
  }

  const response = await fetchWithTimeout(
    `${path}${buildQuery(options.query)}`,
    { method: options.method ?? 'GET', headers, body },
    options.timeoutMs ?? REQUEST_TIMEOUT_MS,
  )

  if (!response.ok) {
    throw await parseError(response)
  }

  if (response.status === 204) {
    return undefined as T
  }

  const text = await response.text()
  if (!text) {
    return undefined as T
  }

  try {
    return JSON.parse(text) as T
  } catch {
    return text as unknown as T
  }
}

async function fetchRaw(path: string, query?: Record<string, QueryValue>): Promise<Response> {
  const response = await fetchWithTimeout(
    `${path}${buildQuery(query)}`,
    { method: 'GET', headers: { ...authHeaders() } },
    REQUEST_TIMEOUT_MS,
  )

  if (!response.ok) {
    throw await parseError(response)
  }

  return response
}

const filenameFromResponse = (response: Response, fallback: string): string => {
  const disposition = response.headers.get('content-disposition') ?? ''
  const utf8Match = disposition.match(/filename\*=UTF-8''([^;]+)/i)
  if (utf8Match?.[1]) {
    try {
      return decodeURIComponent(utf8Match[1])
    } catch {
      return fallback
    }
  }

  const match = disposition.match(/filename="?([^";]+)"?/i)
  return match?.[1] || fallback
}

const saveBlob = (blob: Blob, filename: string) => {
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = filename
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
  URL.revokeObjectURL(url)
}

const download = async (
  path: string,
  fallbackFilename: string,
  query?: Record<string, QueryValue>,
): Promise<void> => {
  const response = await fetchRaw(path, query)
  const blob = await response.blob()
  saveBlob(blob, filenameFromResponse(response, fallbackFilename))
}

const timestampSuffix = (): string => {
  const now = new Date()
  const pad = (value: number) => String(value).padStart(2, '0')
  return `${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}-${pad(now.getHours())}${pad(now.getMinutes())}${pad(now.getSeconds())}`
}

export const normalizeLogLines = (response: LogLinesResponse | string | undefined): string[] => {
  if (!response) return []

  if (typeof response === 'string') {
    return response.split('\n')
  }

  if (Array.isArray(response.lines)) return response.lines
  if (typeof response.lines === 'string') return response.lines.split('\n')
  if (typeof response.log === 'string') return response.log.split('\n')
  if (typeof response.content === 'string') return response.content.split('\n')

  return []
}

export const waitForJob = async (
  jobId: string,
  onProgress?: (job: JobResponse) => void,
  options: { intervalMs?: number; timeoutMs?: number } = {},
): Promise<JobResponse> => {
  const interval = options.intervalMs ?? 800
  const deadline = Date.now() + (options.timeoutMs ?? 10 * 60 * 1000)

  for (;;) {
    const job = await mproxyApi.getJob(jobId)
    onProgress?.(job)

    if (job.state !== 'running') return job
    if (Date.now() > deadline) {
      throw new MProxyApiError('任务等待超时', { code: 'job-timeout' })
    }

    await new Promise((resolve) => setTimeout(resolve, interval))
  }
}

export const mproxyApi = {
  // 5.1 status
  health: () => request<HealthResponse>('/healthz'),
  status: () => request<MProxyStatus>('/api/status'),
  meta: () => request<MetaResponse>('/api/meta'),

  // 5.2 config
  getConfig: () => request<ConfigResponse>('/api/config'),
  validateConfig: (yaml: string) =>
    request<ValidateConfigResponse>('/api/config/validate', {
      method: 'POST',
      body: { yaml },
      timeoutMs: 60_000,
    }),
  saveConfig: (yaml: string, apply: ApplyMode = 'auto') =>
    request<SaveConfigResponse>('/api/config', {
      method: 'PUT',
      body: { yaml, apply },
      timeoutMs: 120_000,
    }),
  importConfig: (payload: {
    url?: string
    content?: string
    mode: ImportMode
    apply?: ApplyMode
  }) =>
    request<ImportConfigResponse>('/api/config/import', {
      method: 'POST',
      body: payload,
      timeoutMs: 180_000,
    }),
  exportConfig: () => download('/api/config/export', `mproxy-config-${timestampSuffix()}.yaml`),
  resetConfig: () =>
    request<{ ok: boolean }>('/api/config/reset', {
      method: 'POST',
      body: { confirm: true },
      timeoutMs: 60_000,
    }),
  backupConfig: () => download('/api/config/backup', `mproxy-backup-${timestampSuffix()}.yaml`),
  restoreConfig: () =>
    request<{ ok: boolean }>('/api/config/restore', {
      method: 'POST',
      body: {},
      timeoutMs: 120_000,
    }),

  // 5.3 core / jobs / logs / backend / packages
  startCore: () =>
    request<{ ok: boolean; status: CoreStatus }>('/api/core/start', { method: 'POST' }),
  stopCore: () =>
    request<{ ok: boolean; status: CoreStatus }>('/api/core/stop', { method: 'POST' }),
  restartCore: () =>
    request<{ ok: boolean; status: CoreStatus }>('/api/core/restart', { method: 'POST' }),
  reloadCore: () =>
    request<{ ok: boolean; status: CoreStatus }>('/api/core/reload', { method: 'POST' }),
  checkCoreUpdate: (channel: UpdateChannel = 'stable') =>
    request<CoreUpdateCheckResponse>('/api/core/update/check', { query: { channel } }),
  updateCore: (channel: UpdateChannel = 'stable') =>
    request<{ jobId: string }>('/api/core/update', {
      method: 'POST',
      body: { channel },
      timeoutMs: 60_000,
    }),
  getJob: (jobId: string) => request<JobResponse>(`/api/jobs/${encodeURIComponent(jobId)}`),
  getCoreLogs: (lines = 300) => request<LogLinesResponse>('/api/logs/core', { query: { lines } }),
  clearCoreLogs: () => request<{ ok: boolean }>('/api/logs/core/clear', { method: 'POST' }),
  getBackendLogs: (lines = 300) =>
    request<LogLinesResponse>('/api/logs/backend', { query: { lines } }),
  restartBackend: () => request<{ ok: boolean }>('/api/backend/restart', { method: 'POST' }),
  shutdownBackend: () => request<{ ok: boolean }>('/api/backend/shutdown', { method: 'POST' }),
  getPackages: (type: 'user' | 'system' | 'all' = 'user') =>
    request<PackagesResponse>('/api/packages', { query: { type } }),

  // 5.4 MProxyEvolution page
  getSettings: () => request<MProxySettings>('/api/mproxy/settings'),
  patchSettings: (patch: MProxySettingsPatch) =>
    request<MProxySettingsPatchResponse>('/api/mproxy/settings', {
      method: 'PATCH',
      body: patch,
      timeoutMs: 120_000,
    }),
  getApps: () => request<MProxyAppsResponse>('/api/mproxy/apps'),
  putApps: (mode: AppsMode, apps: string[]) =>
    request<MProxyAppsPutResponse>('/api/mproxy/apps', {
      method: 'PUT',
      body: { mode, apps },
      timeoutMs: 120_000,
    }),
  addProvider: (provider: MProxyProviderInput) =>
    request<MProxyProviderResponse>('/api/mproxy/providers', {
      method: 'POST',
      body: provider,
      timeoutMs: 60_000,
    }),
  deleteProvider: (name: string) =>
    request<MProxyProviderResponse>(`/api/mproxy/providers/${encodeURIComponent(name)}`, {
      method: 'DELETE',
      timeoutMs: 60_000,
    }),
  getDiagnostics: () => request<DiagnosticsResponse>('/api/mproxy/diagnostics'),
  downloadDiagnostics: () =>
    download('/api/mproxy/diagnostics', `mproxy-diagnostics-${timestampSuffix()}.json`),

  // 5.5 mihomo reverse proxy (zashboard's own traffic path)
  getMihomoProviders: () => request<MihomoProvidersResponse>('/api/mihomo/providers/proxies'),
  updateAllProviders: () =>
    request<unknown>('/api/mihomo/providers/proxies', {
      method: 'PUT',
      timeoutMs: 120_000,
    }),
}

export default mproxyApi
