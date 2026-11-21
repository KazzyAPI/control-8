// Text output as code (UI-003: lines are React text; nothing is interpreted). Both views are bounded and scroll inside.

// daisyUI `mockup-code`.
export function MockCode({ text, label, prefix, maxHeight = '22rem' }: { text: string; label: string; prefix?: string; maxHeight?: string }) {
  const lines = text.split('\n')
  return (
    <div className="mockup-code w-full overflow-auto text-sm" style={{ maxHeight }} tabIndex={0} role="region" aria-label={label}>
      {lines.map((l, i) => <pre key={i} data-prefix={prefix ?? String(i + 1)}><code>{l}</code></pre>)}
    </div>
  )
}

// Two manifests compared with the daisyUI `diff` wipe. The aspect ratio is derived from the longer text so the panel fits its content,
// capped (max-h) so a huge manifest scrolls inside each pane rather than growing the page.
export function DiffView({ a, b, an, bn }: { a: string; b: string; an: number; bn: number }) {
  if (a === b) return <p className="text-sm">No changes between revision {an} and revision {bn}.</p>
  const rows = Math.min(Math.max(a.split('\n').length, b.split('\n').length, 4), 24)
  const pane = (t: string) => <div className="bg-base-200 text-base-content p-4 overflow-auto h-full"><pre className="text-sm font-mono">{t}</pre></div>
  return <>
    <p className="text-sm text-base-content/75">Drag the handle to wipe between revision {an} (left) and revision {bn} (right).</p>
    <figure className="diff w-full rounded-box border border-base-300" style={{ height: `${rows * 1.4 + 2.5}rem`, maxHeight: '32rem' }} role="group" aria-label={`Manifest diff, revision ${an} against revision ${bn}`}>
      <div className="diff-item-1">{pane(a)}</div><div className="diff-item-2">{pane(b)}</div><div className="diff-resizer" />
    </figure>
  </>
}
