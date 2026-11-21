import { useQueries } from '@tanstack/react-query'
import { useState, type ReactNode } from 'react'
import { Link, NavLink, useLocation } from 'react-router'
import { get, remaining, useGet, useNow, type S } from '../lib'
import { AsyncBtn } from '../ui'
import { applyTheme, isDark, readTheme, THEMES, type Theme } from '../theme'
import { Icon, ICONS } from './Parts'

export type Me = S['Principal']

// Nav badges: pending approvals (deployments + grant requests you can decide), active grants, drifted stacks.
// Same query keys as the screens, so this adds no extra requests once a screen has loaded.
export function useNavCounts(me: Me) {
  const inv = useGet<{ items: S['Stack'][] }>(['inventory'], '/inventory')
  const grants = useGet<{ items: S['AccessGrant'][] }>(['grants', 'all'], '/access-grants')
  const now = useNow(5000)
  const stacks = inv.data?.items ?? []
  const lists = useQueries({ queries: stacks.map((s) => ({ queryKey: ['deployments', s.namespace, s.slug],
    queryFn: () => get<{ items: S['Deployment'][] }>(`/namespaces/${s.namespace}/stacks/${s.slug}/deployments`, { path: { ns: s.namespace, stack: s.slug } }) })) })
  const gs = grants.data?.items ?? []
  const deploys = lists.flatMap((l) => l.data?.items ?? []).filter((d) => d.status === 'pending_approval').length
  const requests = gs.filter((g) => g.status === 'requested' && (me.global_admin || me.approver_for?.includes(g.namespace ?? ''))).length
  return {
    approvals: deploys, requests,
    active: gs.filter((g) => g.status === 'active' && remaining(g.expires_at, now)).length,
    drift: stacks.filter((s) => s.health === 'drifted' || (s.drift?.length ?? 0) > 0).length,
  }
}
export type Counts = ReturnType<typeof useNavCounts>

type Item = { to: string; text: string; count?: number; what?: string }
const items = (me: Me, c: Counts): [string, Item[]][] => [
  ['Workspace', [{ to: '/', text: 'Overview', count: c.drift, what: 'stacks with drift' }, { to: '/approvals', text: 'Approvals', count: c.approvals, what: 'deployments waiting' }, { to: '/audit', text: 'Audit' }]],
  ['Prod access', [{ to: '/access', text: 'My grants' }, { to: '/access/request', text: 'Request access' },
    ...(me.global_admin || me.approver_for?.length ? [{ to: '/access/approvals', text: 'Approver queue', count: c.requests, what: 'requests waiting' }] : []),
    { to: '/access/active', text: 'Active grants', count: c.active, what: 'active' }]],
  ...(me.global_admin ? [['Admin', [{ to: '/admin/access', text: 'Admin: access' }, { to: '/admin/policy', text: 'Admin: policy' }, { to: '/admin/targets', text: 'Targets' }]] as [string, Item[]]] : []),
]

// daisyUI indicator: a count badge on the corner of the label; the count is also read out.
const Badge = ({ n, what }: { n?: number; what?: string }) => n ? <span className="indicator-item badge badge-secondary badge-sm">{n}<span className="sr-only"> {what}</span></span> : null

export function Sidebar({ me, counts }: { me: Me; counts: Counts }) {
  return (
    <nav aria-label="Main" className="bg-base-200 text-base-content min-h-full w-64 flex flex-col">
      <Link to="/" className="flex items-center gap-2 px-6 py-4 text-xl font-bold"><Icon d={ICONS.shield} className="size-6 text-primary" />Control 8</Link>
      <ul className="menu w-full px-3 gap-1">
        {items(me, counts).map(([group, list]) => <li key={group}>
          <span className="menu-title text-base-content mt-3 text-xs uppercase">{group}</span>
          <ul>{list.map((i) => <li key={i.to}><NavLink to={i.to} end className={({ isActive }) => (isActive ? 'menu-active' : '')}>
            <span className="indicator pr-5">{i.text}<Badge n={i.count} what={i.what} /></span></NavLink></li>)}</ul>
        </li>)}
      </ul>
    </nav>
  )
}

// Bottom navigation on small screens (the sidebar is a drawer there).
export function BottomDock({ counts, onMenu }: { counts: Counts; onMenu: () => void }) {
  const l = (to: string, text: string, icon: string, n?: number, what?: string) =>
    <NavLink to={to} end className={({ isActive }) => (isActive ? 'dock-active' : '')}>
      <span className="indicator"><Icon d={icon} /><Badge n={n} what={what} /></span><span className="dock-label">{text}</span></NavLink>
  return (
    <nav aria-label="Quick navigation" className="dock dock-sm lg:hidden z-30">
      {l('/', 'Overview', ICONS.home, counts.drift, 'stacks with drift')}{l('/approvals', 'Approvals', ICONS.check, counts.approvals, 'deployments waiting')}
      {l('/access', 'Grants', ICONS.key)}{l('/audit', 'Audit', ICONS.list)}
      <button type="button" onClick={onMenu}><Icon d={ICONS.menu} /><span className="dock-label">Menu</span></button>
    </nav>
  )
}

// ---- breadcrumbs derived from the route ---------------------------------------------------------------------------------
const LABELS: Record<string, string> = { '/approvals': 'Approvals', '/access': 'My grants', '/access/active': 'Active grants', '/access/request': 'Request access',
  '/access/approvals': 'Approver queue', '/audit': 'Audit', '/admin/access': 'Access administration', '/admin/policy': 'Global policy', '/admin/targets': 'Targets' }
export function crumbs(path: string): [string, string?][] {
  const p = path.replace(/\/+$/, '') || '/'
  const home: [string, string] = ['Overview', '/']
  if (p === '/') return [['Overview']]
  const m = p.split('/').slice(1)
  if (m[0] === 'n' && m[1]) {
    const out: [string, string?][] = [home, [m[1], m.length > 2 ? `/n/${m[1]}` : undefined]]
    if (m[2] === 'assets' && m[3]) out.push([m[3]])
    if (m[2] === 's' && m[3]) {
      out.push([m[3], m.length > 4 ? `/n/${m[1]}/s/${m[3]}` : undefined])
      if (m[4] === 'revisions') out.push([m[5] === 'new' ? 'New revision' : `Revision ${m[5]}`])
    }
    return out
  }
  if (m[0] === 'deployments' && m[1]) return [home, [`Deployment ${m[1]}`]]
  return LABELS[p] ? [home, [LABELS[p]]] : [home, ['Not found']]
}
export function Breadcrumbs() {
  const c = crumbs(useLocation().pathname)
  return (
    <nav aria-label="Breadcrumb" className="breadcrumbs text-sm min-w-0 py-0">
      <ul>{c.map(([text, to], i) => <li key={i}>{to ? <Link to={to}>{text}</Link> : <span aria-current={i === c.length - 1 ? 'page' : undefined}>{text}</span>}</li>)}</ul>
    </nav>
  )
}

// ---- theme switcher: swap icon + dropdown of theme-controller radios ------------------------------------------------------
const cap = (s: string) => s[0].toUpperCase() + s.slice(1)
export function ThemeSwitcher() {
  const [t, setT] = useState<Theme>(readTheme)
  const pick = (x: Theme) => { setT(x); applyTheme(x, true) }
  return (
    <div className="dropdown dropdown-end">
      <div tabIndex={0} role="button" className="btn btn-ghost btn-circle" aria-label="Choose theme">
        <span className={`swap swap-rotate ${isDark(t) ? 'swap-active' : ''}`}><Icon d={ICONS.sun} className="swap-off size-5" /><Icon d={ICONS.moon} className="swap-on size-5" /></span>
      </div>
      <fieldset tabIndex={0} className="dropdown-content bg-base-200 text-base-content rounded-box shadow-lg z-50 w-44 p-2 mt-2 border border-base-300">
        <legend className="sr-only">Theme</legend>
        {(['system', ...THEMES] as Theme[]).map((x) => <input key={x} type="radio" name="theme" value={x} aria-label={x === 'system' ? 'System default' : cap(x)}
          className={`${x === 'system' ? '' : 'theme-controller '}btn btn-sm btn-block btn-ghost justify-start`} checked={t === x} onChange={() => pick(x)} />)}
      </fieldset>
    </div>
  )
}

// ---- user menu ------------------------------------------------------------------------------------------------------------------
export function UserMenu({ me }: { me: Me }) {
  const bg = me.auth_method === 'break_glass'
  const out = async () => {
    try { await fetch('/auth/logout', { method: 'POST', credentials: 'same-origin', headers: { 'X-CSRF-Token': me.csrf_token ?? '' } }) } finally { window.location.assign('/login') }
  }
  const initials = (me.display_name || '?').trim().split(/\s+/).map((w) => w[0]).slice(0, 2).join('').toUpperCase()
  return (
    <div className="dropdown dropdown-end">
      <div tabIndex={0} role="button" className="btn btn-ghost rounded-full gap-2 px-1" aria-haspopup="true">
        <span className="avatar avatar-placeholder"><span className="bg-neutral text-neutral-content w-9 rounded-full"><span aria-hidden="true">{initials}</span></span></span>
        <span className="who max-md:sr-only font-medium">{me.display_name}</span>
        {bg && <span className="badge badge-error badge-sm max-sm:sr-only">Break-glass</span>}
      </div>
      <div tabIndex={0} className="dropdown-content card card-sm bg-base-100 text-base-content shadow-lg border border-base-300 z-50 w-64 mt-2">
        <div className="card-body gap-2">
          <p className="font-semibold my-0 break-words">{me.display_name}</p>
          <p className="text-sm my-0">Signed in via {me.auth_method === 'break_glass' ? 'password' : me.auth_method === 'oidc' ? 'OIDC' : me.auth_method}</p>
          <div className="flex flex-wrap gap-1">
            {bg && <span className="badge badge-error">Break-glass</span>}{me.global_admin && <span className="badge badge-neutral">Global admin</span>}
            {(me.approver_for?.length ?? 0) > 0 && <span className="badge badge-outline">Approver</span>}
          </div>
          <div className="divider my-0" />
          <AsyncBtn className="btn btn-outline btn-sm" onClick={out}>Sign out</AsyncBtn>
        </div>
      </div>
    </div>
  )
}

export const Footer = ({ children }: { children?: ReactNode }) => (
  <footer className="footer footer-center sm:footer-horizontal bg-base-200 text-base-content p-4 pb-20 lg:pb-4">
    <aside className="sm:justify-self-start"><p className="my-0">Control 8 {'·'} UI v{__C8_VERSION__}</p></aside>
    <nav className="sm:justify-self-end"><p className="my-0">Navigate with <kbd className="kbd kbd-sm">Tab</kbd> {'·'} close dialogs with <kbd className="kbd kbd-sm">Esc</kbd></p>{children}</nav>
  </footer>
)
