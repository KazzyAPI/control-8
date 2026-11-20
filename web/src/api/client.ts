import createClient from 'openapi-fetch'
import type { paths } from './schema'

let csrf: string | null = null
export const setCsrf = (t: string | null) => { csrf = t }

// UI-002: mutations carry X-CSRF-Token from GET /me.
export const api = createClient<paths>({ baseUrl: `${globalThis.location?.origin ?? ''}/api/v1` }) // absolute so tests (jsdom) can build Requests
api.use({
  onRequest({ request }) {
    if (csrf && !['GET', 'HEAD'].includes(request.method)) request.headers.set('X-CSRF-Token', csrf)
    return request
  },
})
