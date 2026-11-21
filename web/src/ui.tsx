import { useQueryClient } from '@tanstack/react-query'
import { useCallback } from 'react'
import { useNavigate } from 'react-router'
import { api } from './api/client'
import type { S } from './lib'
import { useToast } from './components/ui/Toast'

// Behaviour shared by every mutation. Presentation lives in components/ui (the design system); these re-exports keep one import path.
export { ToastProvider, useToast } from './components/ui/Toast'
export { AsyncBtn, Btn } from './components/ui/Buttons'
export { FormError, FormField as Field } from './components/ui/FormField'
export { ConfirmDialog, Modal as Dialog } from './components/ui/Modal'

// API errors are problem+json (API-003); the code and title surface as toasts.
export class ApiError extends Error { constructor(public problem: Partial<S['Problem']>) { super(problem.detail || problem.title || `HTTP ${problem.status}`) } }
export async function send<T = any>(method: 'POST' | 'PUT' | 'PATCH' | 'DELETE', path: string, opts: { params?: object; body?: unknown } = {}): Promise<T> {
  const { data, error, response } = await (api as any)[method](path, opts)
  if (!response.ok) throw new ApiError(error && typeof error === 'object' ? { status: response.status, ...error } : { status: response.status, title: response.statusText || 'Request failed', code: 'http_error' })
  return (data ?? {}) as T // 204 has no body; callers test for undefined to detect failure
}

// Runs a mutation: errors become toasts (grant_required also opens the request form), success shows a toast and invalidates `inv` keys.
export function useAct() {
  const { toast } = useToast()
  const nav = useNavigate()
  const qc = useQueryClient()
  return useCallback(async <T,>(fn: () => Promise<T>, ok?: string, inv: unknown[][] = []): Promise<T | undefined> => {
    try {
      const r = await fn()
      if (ok) toast(ok)
      for (const queryKey of inv) qc.invalidateQueries({ queryKey })
      return r
    } catch (e) {
      const p = e instanceof ApiError ? e.problem : { title: String(e) }
      toast(`${p.title ?? 'Error'}${p.detail ? `: ${p.detail}` : ''}${p.code ? ` (${p.code})` : ''}`, 'error')
      if (p.code === 'grant_required' && p.scope) nav(`/access/request?namespace=${p.scope.namespace ?? ''}&target=${p.scope.target ?? ''}`)
      return undefined
    }
  }, [toast, nav, qc])
}
