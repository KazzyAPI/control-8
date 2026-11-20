import { expect, it } from 'vitest'
import { approveErr, denyErr, deployErr, grantErr, revisionErr, slugErr } from './validate'

it('UI-013: deploy needs the matching profile, the typed slug on prod, the checkbox for as_is', () => {
  const ok = { profile: 'managed_rollout', picked: 'managed_rollout', tier: 'prod', typed: 'shop', slug: 'shop', ack: false }
  expect(deployErr(ok)).toBeNull()
  expect(deployErr({ ...ok, picked: '' })).toMatch(/Choose/)
  expect(deployErr({ ...ok, picked: 'as_is' })).toMatch(/does not match/)
  expect(deployErr({ ...ok, typed: 'sho' })).toMatch(/Type "shop"/)
  expect(deployErr({ ...ok, tier: 'non-prod', typed: '' })).toBeNull()
  const asIs = { ...ok, profile: 'as_is', picked: 'as_is' }
  expect(deployErr(asIs)).toMatch(/automatic rollback/)
  expect(deployErr({ ...asIs, ack: true })).toBeNull()
})

it('09: grant request bounds (reason 10..500, ticket <= 100, duration >= 15)', () => {
  const ok = { namespace: 'shop', target: 'prod-eu', reason: 'Release 1.4 of shop', ticket_ref: '', duration: 60 }
  expect(grantErr(ok)).toBeNull()
  expect(grantErr({ ...ok, reason: 'short' })).toMatch(/10 to 500/)
  expect(grantErr({ ...ok, reason: 'x'.repeat(501) })).toMatch(/10 to 500/)
  expect(grantErr({ ...ok, ticket_ref: 'x'.repeat(101) })).toMatch(/Ticket/)
  expect(grantErr({ ...ok, duration: 14 })).toMatch(/15/)
  expect(grantErr({ ...ok, namespace: '' })).toMatch(/Choose/)
})

it('GRT-025: approver may shorten, never lengthen; deny needs a note', () => {
  expect(approveErr(30, 60)).toBeNull()
  expect(approveErr(60, 60)).toBeNull()
  expect(approveErr(61, 60)).toMatch(/longer/)
  expect(approveErr(5, 60)).toMatch(/15/)
  expect(denyErr(' ')).toMatch(/required/)
  expect(denyErr('no ticket')).toBeNull()
})

it('06: revision and slug validation', () => {
  expect(revisionErr({ profile: 'as_is', manifests: 'kind: Pod', timeout: '', digest_poll: false })).toBeNull()
  expect(revisionErr({ profile: '', manifests: 'x', timeout: '', digest_poll: false })).toMatch(/profile/)
  expect(revisionErr({ profile: 'as_is', manifests: ' ', timeout: '', digest_poll: false })).toMatch(/Manifests/)
  expect(revisionErr({ profile: 'as_is', manifests: 'x', timeout: '0', digest_poll: false })).toMatch(/timeout/)
  expect(revisionErr({ profile: 'as_is', manifests: 'x', timeout: '', digest_poll: true })).toMatch(/managed/)
  expect(slugErr('shop-1')).toBeNull()
  expect(slugErr('Shop')).not.toBeNull()
})
