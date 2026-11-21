import { render, screen } from '@testing-library/react'
import { expect, it } from 'vitest'
import { ProfileBadge, TierBadge } from './Badges'

it('UI-010/015/032 badges always carry text', () => {
  render(<><ProfileBadge profile="as_is" /><ProfileBadge profile={null} /><TierBadge tier="prod" /><TierBadge tier="non-prod" /></>)
  for (const t of [/As-is/, /No profile yet/, /PROD/, /non-prod/]) expect(screen.getByText(t)).toBeTruthy()
})
