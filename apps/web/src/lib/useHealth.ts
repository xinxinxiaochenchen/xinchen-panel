import { useEffect, useState } from 'react'

export type HealthState = {
  status: 'checking' | 'online' | 'offline'
  checkedAt: Date | null
}

export function useHealth(): HealthState {
  const [state, setState] = useState<HealthState>({ status: 'checking', checkedAt: null })

  useEffect(() => {
    let active = true
    let request: AbortController | null = null
    const check = async () => {
      request?.abort()
      request = new AbortController()
      const timeout = window.setTimeout(() => request?.abort(), 4000)
      try {
        const response = await fetch('/api/v1/health/ready', { signal: request.signal, cache: 'no-store' })
        if (active) setState({ status: response.ok ? 'online' : 'offline', checkedAt: new Date() })
      } catch {
        if (active) setState({ status: 'offline', checkedAt: new Date() })
      } finally {
        window.clearTimeout(timeout)
      }
    }
    void check()
    const interval = window.setInterval(() => void check(), 15000)
    return () => {
      active = false
      request?.abort()
      window.clearInterval(interval)
    }
  }, [])

  return state
}
