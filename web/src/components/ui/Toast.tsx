import { createContext, useCallback, useContext, useRef, useState, type ReactNode } from 'react'
import { Notice, type NoticeTone } from './Notice'

export type ToastKind = 'ok' | 'error' | 'info' | 'warning'
type Toast = { id: number; text: string; kind: ToastKind }
const TONE: Record<ToastKind, NoticeTone> = { ok: 'success', error: 'error', info: 'info', warning: 'warning' }
export const TOAST_MS = 8000
const Ctx = createContext<{ toast: (text: string, kind?: ToastKind) => void; say: (text: string) => void }>({ toast() {}, say() {} })
export const useToast = () => useContext(Ctx)

// UI-031: a daisyUI toast stack (each toast is a Notice: error = role alert, others role status; auto-dismiss, closable) plus one
// polite live region for `say`, at most one announcement per 5 s.
export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([])
  const [said, setSaid] = useState('')
  const last = useRef(0)
  const drop = useCallback((id: number) => setToasts((t) => t.filter((x) => x.id !== id)), [])
  const toast = useCallback((text: string, kind: ToastKind = 'ok') => {
    const id = Date.now() + Math.random()
    setToasts((t) => [...t, { id, text, kind }])
    setTimeout(() => drop(id), TOAST_MS)
  }, [drop])
  const say = useCallback((text: string) => { if (Date.now() - last.current >= 5000) { last.current = Date.now(); setSaid(text) } }, [])
  return (
    <Ctx.Provider value={{ toast, say }}>
      {children}
      <div className="toast toast-end toast-bottom z-50 max-lg:mb-16 max-w-[calc(100vw-1rem)]">
        {toasts.map((t) => <Notice key={t.id} tone={TONE[t.kind]} role={t.kind === 'error' ? 'alert' : 'status'} onDismiss={() => drop(t.id)} className="shadow-lg w-80 max-w-full">{t.text}</Notice>)}
      </div>
      <div className="sr-only" role="status" aria-live="polite">{said}</div>
    </Ctx.Provider>
  )
}
