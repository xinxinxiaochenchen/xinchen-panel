import { useCallback, useEffect, useState } from 'react'
import { loadViewer, type Viewer } from './dashboard'

export type ViewerState = Viewer | { kind: 'checking' } | { kind: 'error'; message: string }

export function useViewer() {
  const [generation, setGeneration] = useState(0)
  const [viewer, setViewer] = useState<ViewerState>({ kind: 'checking' })

  useEffect(() => {
    let active = true
    void loadViewer(window.location.protocol, window.location.hostname)
      .then((result) => { if (active) setViewer(result) })
      .catch(() => { if (active) setViewer({ kind: 'error', message: '账户服务暂不可用，请稍后重试。' }) })
    return () => { active = false }
  }, [generation])

  const refresh = useCallback(() => {
    setViewer({ kind: 'checking' })
    setGeneration((value) => value + 1)
  }, [])

  return { viewer, refresh }
}
