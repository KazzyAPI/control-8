import type { components } from '../api/schema'

type Profile = components['schemas']['SafetyProfile'] | null | undefined
type Tier = components['schemas']['EnvironmentTier']

// UI-010: the only way a safety profile is shown; text is always present (UI-032).
export function ProfileBadge({ profile, large }: { profile: Profile; large?: boolean }) {
  const [cls, icon, text] =
    profile === 'managed_rollout' ? ['managed', '↻', 'Managed rollout']
    : profile === 'as_is' ? ['asis', '⚠', 'As-is — no safety net']
    : profile === 'parachute' ? ['parachute', '⛨', 'With Parachute']
    : ['none', '', 'No profile yet']
  return (
    <span className={`badge profile ${cls}${large ? ' large' : ''}`}>
      {icon && <span aria-hidden="true">{icon} </span>}{text}
    </span>
  )
}

// UI-015
export function TierBadge({ tier }: { tier: Tier }) {
  return tier === 'prod'
    ? <span className="badge tier prod"><span aria-hidden="true">{'🔒'} </span>PROD</span>
    : <span className="badge tier nonprod">non-prod</span>
}
