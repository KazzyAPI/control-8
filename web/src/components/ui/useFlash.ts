import { useEffect, useRef, useState } from 'react'

// UI-024: bumps when `sig` changes after first render; use as the row key and add class `flash` when > 0.
export function useFlash(sig: string) {
  const prev = useRef(sig)
  const [n, setN] = useState(0)
  useEffect(() => { if (prev.current !== sig) { prev.current = sig; setN((x) => x + 1) } }, [sig])
  return n
}
