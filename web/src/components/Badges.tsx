import type { components } from '../api/schema'

type Profile = components['schemas']['SafetyProfile'] | null | undefined
type Tier = components['schemas']['EnvironmentTier']

const TIP: Record<string, string> = {
  managed_rollout: 'Managed rollout: Control 8 rolls out gradually, watches health and rolls back automatically.',
  as_is: 'As-is: applied exactly as written with no safety net and no automatic rollback.',
  parachute: 'With Parachute: an external rollback agent protects this stack.',
}

// UI-010: the only way a safety profile is shown; text is always present (UI-032). The tooltip explains the profile.
export function ProfileBadge({ profile, large }: { profile: Profile; large?: boolean }) {
  const [cls, icon, text] =
    profile === 'managed_rollout' ? ['badge-primary', '↻', 'Managed rollout']
    : profile === 'as_is' ? ['badge-warning border-2 border-dashed border-base-content', '⚠', 'As-is — no safety net']
    : profile === 'parachute' ? ['badge-secondary', '⛨', 'With Parachute']
    : ['badge-outline', '', 'No profile yet']
  const tip = TIP[profile ?? '']
  const badge = (
    <span className={`badge profile ${cls}${large ? ' large badge-lg' : ''}`} tabIndex={tip && large ? 0 : undefined}>
      {icon && <span aria-hidden="true">{icon} </span>}{text}
    </span>
  )
  return tip ? <span className="tooltip tooltip-bottom before:max-w-64 before:whitespace-normal" data-tip={tip}>{badge}</span> : badge
}

// UI-015
export function TierBadge({ tier, large }: { tier: Tier; large?: boolean }) {
  const size = large ? ' badge-lg' : ''
  return tier === 'prod'
    ? <span className={`badge badge-error font-bold${size}`}><span aria-hidden="true">{'🔒'} </span>PROD</span>
    : <span className={`badge badge-outline${size}`}>non-prod</span>
}

// Health/status words as text on a colour with a status dot (never colour alone, UI-032).
// Full class names (not built from a tone word) so Tailwind's scanner sees and generates them.
const B = { success: 'badge-success', warning: 'badge-warning', info: 'badge-info', error: 'badge-error' }
const TONE: Record<string, keyof typeof B> = { healthy: 'success', succeeded: 'success', success: 'success', active: 'success', approved: 'success',
  degraded: 'warning', pending_approval: 'warning', requested: 'warning', progressing: 'info', running: 'info', queued: 'info',
  failed: 'error', denied: 'error', rejected: 'error', revoked: 'error', missing: 'error', unreachable: 'error' }
export const StatusBadge = ({ text }: { text?: string | null }) => {
  const tone = TONE[text ?? '']
  return <span className={`badge ${tone ? B[tone] : 'badge-ghost'}`}><span className="status bg-current" aria-hidden="true" />{text ?? 'unknown'}</span>
}
