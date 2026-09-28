export function readCSRFToken(cookies: string, protocol: string): string {
  const names = protocol === 'https:'
    ? ['__Host-control_csrf', 'control_csrf']
    : ['control_csrf', '__Host-control_csrf']
  const entries = cookies.split(';').map((entry) => entry.trim())
  for (const name of names) {
    const entry = entries.find((value) => value.startsWith(`${name}=`))
    if (entry) return entry.slice(name.length + 1)
  }
  return ''
}

export function sessionTransportLabel(protocol: string): string {
  return protocol === 'https:' ? 'HTTPS 加密连接' : 'HTTP 会话'
}
