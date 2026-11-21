import type { components } from '../api/schema'

type Profile = components['schemas']['SafetyProfile'] | null | undefined
type Tier = components['schemas']['EnvironmentTier']

// UI-010: the only way a safety profile is shown; text is always present (UI-032).
export function ProfileBadge({ profile, large }: { profile: Profile; large?: boolean }) {
  const [cls, icon, text] =
    profile === 'managed_rollout' ? ['badge-primary', '↻', 'Managed rollout']
    : profile === 'as_is' ? ['badge-warning border-2 border-dashed border-base-content', '⚠', 'As-is — no safety net']
    : profile === 'parachute' ? ['badge-secondary', '⛨', 'With Parachute']
    : ['badge-outline', '', 'No profile yet']
  return (
    <span className={`badge profile ${cls}${large ? ' large badge-lg' : ''}`}>
      {icon && <span aria-hidden="true">{icon} </span>}{text}
    </span>
  )
}

// UI-015
export function TierBadge({ tier }: { tier: Tier }) {
  return tier === 'prod'
    ? <span className="badge badge-error font-bold"><span aria-hidden="true">{'🔒'} </span>PROD</span>
    : <span className="badge badge-outline">non-prod</span>
}

// Health/status words as text on a colour (never colour alone, UI-032).
const TONE: Record<string, string> = { healthy: 'badge-success', succeeded: 'badge-success', success: 'badge-success', active: 'badge-success', approved: 'badge-success',
  degraded: 'badge-warning', pending_approval: 'badge-warning', requested: 'badge-warning', progressing: 'badge-info', running: 'badge-info', queued: 'badge-info',
  failed: 'badge-error', denied: 'badge-error', rejected: 'badge-error', revoked: 'badge-error', missing: 'badge-error', unreachable: 'badge-error' }
export const StatusBadge = ({ text }: { text?: string | null }) => <span className={`badge ${TONE[text ?? ''] ?? 'badge-ghost'}`}>{text ?? 'unknown'}</span>
