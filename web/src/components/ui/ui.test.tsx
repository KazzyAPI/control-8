import { fireEvent, render, screen } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { initials } from './Avatar'
import { ConfirmDialog } from './Modal'
import { splitDuration } from './Time'

it('splitDuration clamps at zero and splits h/m/s', () => {
  expect(splitDuration(-5)).toEqual({ h: 0, m: 0, s: 0 })
  expect(splitDuration(3_725_000)).toEqual({ h: 1, m: 2, s: 5 })
})

it('initials uses up to two words and a fallback', () => {
  expect(initials('Bob Admin')).toBe('BA')
  expect(initials('ann')).toBe('A')
  expect(initials(null)).toBe('?')
})

it('ConfirmDialog keeps confirm blocked until the typed text matches', () => {
  const onConfirm = vi.fn(async () => true)
  render(<ConfirmDialog title="Tear down" confirmLabel="Tear down now" typed="shop" reason={false} onClose={() => {}} onConfirm={onConfirm} />)
  const go = screen.getByRole('button', { name: 'Tear down now' })
  fireEvent.click(go)
  expect(onConfirm).not.toHaveBeenCalled()
  fireEvent.change(screen.getByRole('textbox'), { target: { value: 'shop' } })
  fireEvent.click(go)
  expect(onConfirm).toHaveBeenCalled()
})
