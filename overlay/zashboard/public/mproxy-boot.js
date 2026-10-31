/**
 * MihomoProxy Evolution boot script.
 *
 * Runs before the zashboard bundle when the panel is served by the MihomoProxy Evolution Go
 * backend. It consumes the `?token=` / `#token=` hand-off produced by
 * `mproxy boot`, exposes the token to the app, and points zashboard at the
 * same-origin `/api/mihomo` reverse proxy.
 *
 * Everything here is best-effort: if the page is opened without a token we
 * keep it alive so the MihomoProxy Evolution page can explain what to do.
 */
;(function () {
  'use strict'

  // 尽量早地打开 edge-to-edge，并请求 KernelSU 的 /internal/insets.css。
  // 后者会触发宿主把 WebView 铺到系统栏下，并注入 --safe-area-inset-* /
  // --window-inset-* 变量；zashboard 的源码已改为优先使用这些变量。
  try {
    if (window.ksu && typeof window.ksu.enableEdgeToEdge === 'function') {
      window.ksu.enableEdgeToEdge(true)
    } else if (window.ksu && typeof window.ksu.enableInsets === 'function') {
      window.ksu.enableInsets(true)
    }
  } catch (error) {
    // 老版本管理器没有该接口
  }
  try {
    var insetsLink = document.createElement('link')
    insetsLink.rel = 'stylesheet'
    insetsLink.type = 'text/css'
    insetsLink.href = '/internal/insets.css'
    ;(document.head || document.documentElement).appendChild(insetsLink)
  } catch (error) {
    // ignore
  }

  var TOKEN_STORAGE_KEY = 'mproxy/token'
  var API_LIST_KEY = 'setup/api-list'
  var ACTIVE_UUID_KEY = 'setup/active-uuid'
  var BACKEND_UUID = 'mproxy-local'

  var safeGet = function (storage, key) {
    try {
      return storage.getItem(key)
    } catch (error) {
      return null
    }
  }

  var safeSet = function (storage, key, value) {
    try {
      storage.setItem(key, value)
      return true
    } catch (error) {
      return false
    }
  }

  var decode = function (value) {
    try {
      return decodeURIComponent(value.replace(/\+/g, ' '))
    } catch (error) {
      return value
    }
  }

  var parseQuery = function (query) {
    var result = {}
    if (!query) return result

    query
      .replace(/^\?/, '')
      .split('&')
      .forEach(function (pair) {
        if (!pair) return
        var index = pair.indexOf('=')
        var key = index === -1 ? pair : pair.slice(0, index)
        var value = index === -1 ? '' : pair.slice(index + 1)
        if (key === 'token') {
          result.token = decode(value)
        }
      })

    return result
  }

  var readToken = function () {
    var search = parseQuery(window.location.search)
    if (search.token) {
      return { token: search.token, from: 'search' }
    }

    var hash = window.location.hash || ''
    var rawHash = hash.replace(/^#/, '')

    if (rawHash.indexOf('token=') === 0) {
      var direct = parseQuery(rawHash)
      if (direct.token) {
        return { token: direct.token, from: 'hash' }
      }
    }

    var queryIndex = rawHash.indexOf('?')
    if (queryIndex !== -1) {
      var hashQuery = parseQuery(rawHash.slice(queryIndex))
      if (hashQuery.token) {
        return { token: hashQuery.token, from: 'hash' }
      }
    }

    return { token: '', from: '' }
  }

  var stripToken = function (token, from) {
    try {
      var url = new URL(window.location.href)

      if (url.searchParams.has('token')) {
        url.searchParams.delete('token')
      }

      if (from === 'hash' || (url.hash && url.hash.indexOf('token=') !== -1)) {
        var hash = url.hash.replace(/^#/, '')
        var queryIndex = hash.indexOf('?')
        var route = queryIndex === -1 ? '' : hash.slice(0, queryIndex)
        var hashQuery = queryIndex === -1 ? hash : hash.slice(queryIndex + 1)
        var params = new URLSearchParams(hashQuery)

        if (params.has('token')) {
          params.delete('token')
          hash = route + (params.toString() ? '?' + params.toString() : '')
        } else if (hash.indexOf('token=') === 0) {
          hash = ''
        }

        url.hash = hash ? '#' + hash : ''
      }

      window.history.replaceState(window.history.state, document.title, url.toString())
    } catch (error) {
      // ignore: replaceState is cosmetic, the token is already captured
    }
  }

  var buildBackend = function (token) {
    return {
      type: 'clash',
      protocol: window.location.protocol.replace(':', ''),
      host: window.location.hostname,
      port: window.location.port || '80',
      secondaryPath: '/api/mihomo',
      password: token,
      uuid: BACKEND_UUID,
      label: 'MihomoProxy Evolution',
    }
  }

  var persist = function (token) {
    if (safeGet(window.sessionStorage, TOKEN_STORAGE_KEY) !== token) {
      safeSet(window.sessionStorage, TOKEN_STORAGE_KEY, token)
    }

    window.__MPROXY_TOKEN__ = token

    // zashboard uses @vueuse/core `useStorage`. Object values (api-list) are
    // JSON-encoded, while string values (active-uuid) are stored raw.
    safeSet(window.localStorage, API_LIST_KEY, JSON.stringify([buildBackend(token)]))
    safeSet(window.localStorage, ACTIVE_UUID_KEY, BACKEND_UUID)
  }

  var boot = function () {
    var result = readToken()

    if (!result.token) {
      // Keep the token handed over by a previous boot in this tab: a plain
      // refresh of http://127.0.0.1:9091/ has no token in the URL.
      var cached = safeGet(window.sessionStorage, TOKEN_STORAGE_KEY) || ''
      if (cached) {
        persist(cached)
      }
      return
    }

    persist(result.token)
    stripToken(result.token, result.from)
  }

  try {
    boot()
  } catch (error) {
    // Never let boot failure block the panel.
    if (window.console && window.console.warn) {
      window.console.warn('[mproxy] boot failed', error)
    }
  }
})()
