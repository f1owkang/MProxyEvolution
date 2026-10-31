import type { Backend } from '@/types'

/**
 * token 只在会话内保存：`public/mproxy-boot.js` 把 hand-off token 写入
 * `sessionStorage['mproxy/token']` 与 `window.__MPROXY_TOKEN__`，
 * localStorage 里的 api-list 不保存真实 token（AGENTS 约定）。
 */
export const MPROXY_BACKEND_UUID = 'mproxy-local'

const TOKEN_STORAGE_KEY = 'mproxy/token'

export const getMPROXYSessionToken = (): string => {
  if (typeof window === 'undefined') return ''

  const fromWindow = window.__MPROXY_TOKEN__
  if (fromWindow) return fromWindow

  try {
    return window.sessionStorage.getItem(TOKEN_STORAGE_KEY) ?? ''
  } catch {
    return ''
  }
}

/**
 * zashboard 的后端配置把 token 存在 `password` 字段；MProxyEvolution 后端的真实
 * token 不落 localStorage，请求时在这里临时解析。
 */
export const resolveBackendPassword = (
  backend?: Pick<Backend, 'uuid' | 'password'> | null,
): string => {
  if (backend && backend.uuid === MPROXY_BACKEND_UUID) return getMPROXYSessionToken()
  return backend?.password ?? ''
}
