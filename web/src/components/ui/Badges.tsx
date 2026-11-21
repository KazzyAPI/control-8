import type { components } from '../../api/schema'
import { Icon } from './Icon'

type Profile = components['schemas']['SafetyProfile'] | null | undefined
type Tier = components['schemas']['EnvironmentTier']

// Wraps any inline element in a daisyUI tooltip (wide tips wrap instead of stretching).
const POS = { top: 'tooltip-top', bottom: 'tooltip-bottom', left: 'tooltip-left', right: 'tooltip-right' }
export const Tip = ({ tip, children, pos = 'bottom' }: { tip: string; children: React.ReactNode; pos?: keyof typeof POS }) =>
  <span className={`tooltip ${POS[pos]} before:max-w-64 before:whitespace-normal before:text-left`} data-tip={tip}>{children}</span>

const PROFILE_TIP: Record<string, string> = {
  managed_rollout: 'Managed rollout: Control 8 rolls out gradually, watches health and rolls back automatically.',
  as_is: 'As-is: applied exactly as written with no safety net and no automatic rollback.',
  parachute: 'With Parachute: an external rollback agent protects this stack.',
}

// UI-010: the only way a safety profile is shown; text is always present (UI-032). The tooltip explains the profile.
export function ProfileBadge({ profile, large }: { profile: Profile; large?: boolean }) {
  const [cls, icon, text] =
    profile === 'managed_rollout' ? ['badge-primary', 'refresh', 'Managed rollout'] as const
    : profile === 'as_is' ? ['badge-warning border-2 border-dashed border-base-content/60', 'warning', 'As-is — no safety net'] as const
    : profile === 'parachute' ? ['badge-secondary', 'shield', 'With Parachute'] as const
    : ['badge-outline', null, 'No profile yet'] as const
  const tip = PROFILE_TIP[profile ?? '']
  const badge = (
    <span className={`badge profile gap-1 ${cls}${large ? ' large badge-lg' : ''}`} tabIndex={tip && large ? 0 : undefined}>
      {icon && <Icon name={icon} className="size-3.5" />}{text}
    </span>
  )
  return tip ? <Tip tip={tip}>{badge}</Tip> : badge
}

// UI-015
export function TierBadge({ tier, large }: { tier: Tier; large?: boolean }) {
  const size = large ? ' badge-lg' : ''
  return tier === 'prod'
    ? <Tip tip="Production: changes need an approved, time-boxed access grant."><span className={`badge badge-error gap-1 font-bold${size}`}><Icon name="lock" className="size-3.5" />PROD</span></Tip>
    : <Tip tip="Non-production: no access grant needed."><span className={`badge badge-outline whitespace-nowrap${size}`}>non-prod</span></Tip>
}

// Health/status words as text on a colour with a status dot (never colour alone, UI-032).
// Full class names (not built from a tone word) so Tailwind's scanner sees and generates them.
const B = { success: 'badge-success', warning: 'badge-warning', info: 'badge-info', error: 'badge-error' }
export type Tone = keyof typeof B
export const TONE: Record<string, Tone> = { healthy: 'success', succeeded: 'success', success: 'success', active: 'success', approved: 'success', yes: 'success', reachable: 'success',
  degraded: 'warning', pending_approval: 'warning', requested: 'warning', drifted: 'warning', progressing: 'info', running: 'info', queued: 'info',
  failed: 'error', denied: 'error', rejected: 'error', revoked: 'error', missing: 'error', unreachable: 'error', no: 'error' }
const STATUS_TIP: Record<string, string> = {
  healthy: 'All workloads are ready.', degraded: 'Some workloads are not ready.', drifted: 'The cluster differs from the desired revision.',
  pending_approval: 'Waiting for an approver.', requested: 'Waiting for an approver.', running: 'In progress.', queued: 'Waiting for a worker.',
  succeeded: 'Finished successfully.', failed: 'Finished with an error.', active: 'Currently in effect.', denied: 'An approver said no.', revoked: 'Ended early.',
}
export const StatusBadge = ({ text, tip }: { text?: string | null; tip?: string }) => {
  const tone = TONE[text ?? '']
  const t = tip ?? STATUS_TIP[text ?? '']
  const badge = <span className={`badge gap-1.5 ${tone ? B[tone] : 'badge-ghost'}`}><span className="status bg-current" aria-hidden="true" />{text ?? 'unknown'}</span>
  return t ? <Tip tip={t}>{badge}</Tip> : badge
}
