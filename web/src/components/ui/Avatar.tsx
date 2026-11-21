// daisyUI avatar with initials (no remote images: CSP). The colour is derived from the name so one person looks the same everywhere.
const TONES = ['bg-primary/20', 'bg-secondary/20', 'bg-accent/20', 'bg-base-300']
const tone = (s: string) => TONES[[...s].reduce((a, c) => a + c.charCodeAt(0), 0) % TONES.length]
export const initials = (name?: string | null) => (name || '?').trim().split(/\s+/).map((w) => w[0]).slice(0, 2).join('').toUpperCase()

// Decorative: the person's name is always written next to it.
export function Avatar({ name, size = 'md', className = '' }: { name?: string | null; size?: 'sm' | 'md' | 'lg'; className?: string }) {
  const sz = size === 'sm' ? 'w-8 text-xs' : size === 'lg' ? 'w-14 text-lg' : 'w-10 text-sm'
  return <span className={`avatar avatar-placeholder ${className}`}><span className={`${tone(name ?? '')} ${sz} aspect-square flex items-center justify-center text-base-content font-semibold rounded-full`}><span aria-hidden="true">{initials(name)}</span></span></span>
}
