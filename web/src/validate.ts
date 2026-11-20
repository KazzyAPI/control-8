// Pure form validators (Vitest): each returns an error message or null.
export const SLUG = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/
export const slugErr = (v: string, what = 'Slug') => (SLUG.test(v) ? null : `${what} must be lowercase letters, digits and dashes`)
export const reqErr = (v: string, what: string) => (v.trim() ? null : `${what} is required`)
// 09: reason 10..500 chars, ticket_ref <= 100, duration >= 15 (the max is enforced by the server, C8_GRANT_MAX).
export function grantErr(f: { namespace: string; target: string; reason: string; ticket_ref: string; duration: number }) {
  if (!f.namespace || !f.target) return 'Choose a namespace and a target'
  if (f.reason.trim().length < 10 || f.reason.length > 500) return 'Reason must be 10 to 500 characters'
  if (f.ticket_ref.length > 100) return 'Ticket reference is at most 100 characters'
  if (!Number.isInteger(f.duration) || f.duration < 15) return 'Duration must be at least 15 minutes'
  return null
}
// GRT-025: an approver may shorten, never lengthen.
export const approveErr = (minutes: number, requested: number) =>
  !Number.isInteger(minutes) || minutes < 15 ? 'Duration must be at least 15 minutes' : minutes > requested ? 'Duration cannot be longer than requested' : null
export const denyErr = (note: string) => (note.trim() ? null : 'A note is required to deny')
// UI-013: right profile radio, typed namespace slug on prod, checkbox for as_is.
export function deployErr(f: { profile: string | null; picked: string; tier: string; typed: string; slug: string; ack: boolean }) {
  if (!f.picked) return 'Choose the safety profile of this revision'
  if (f.picked !== f.profile) return 'The chosen profile does not match this revision'
  if (f.tier === 'prod' && f.typed !== f.slug) return `Type "${f.slug}" to confirm the production deploy`
  if (f.profile === 'as_is' && !f.ack) return 'Confirm that there is no automatic rollback'
  return null
}
export function revisionErr(f: { profile: string; manifests: string; timeout: string; digest_poll: boolean }) {
  if (!f.profile) return 'Choose a safety profile'
  if (!f.manifests.trim()) return 'Manifests are required'
  if (f.timeout && !(/^\d+$/.test(f.timeout) && Number(f.timeout) > 0)) return 'Rollout timeout must be a positive number of seconds'
  if (f.digest_poll && f.profile !== 'managed_rollout') return 'Digest poll needs the managed rollout profile'
  return null
}
export const lines = (v: string) => v.split('\n').map((x) => x.trim()).filter(Boolean)
