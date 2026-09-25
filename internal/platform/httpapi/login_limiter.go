package httpapi

import (
	"strings"
	"sync"
	"time"
)

type loginWindow struct {
	start time.Time
	count int
}

type loginLimiter struct {
	mu       sync.Mutex
	now      func() time.Time
	global   loginWindow
	accounts map[string]loginWindow
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{now: time.Now, accounts: make(map[string]loginWindow)}
}

// Allow limits the total bcrypt workload and repeated guesses for one account.
// The map is capped so arbitrary email addresses cannot grow memory without bound.
func (l *loginLimiter) Allow(email string) time.Duration {
	email = strings.ToLower(strings.TrimSpace(email))
	if len(email) > 254 {
		return time.Minute
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if l.global.start.IsZero() || !now.Before(l.global.start.Add(time.Minute)) {
		l.global = loginWindow{start: now}
	}
	if l.global.count >= 60 {
		return l.global.start.Add(time.Minute).Sub(now)
	}
	account, exists := l.accounts[email]
	if !exists && len(l.accounts) >= 4096 {
		for key, entry := range l.accounts {
			if !now.Before(entry.start.Add(5 * time.Minute)) {
				delete(l.accounts, key)
			}
		}
		if len(l.accounts) >= 4096 {
			return time.Minute
		}
	}
	if !exists || !now.Before(account.start.Add(5*time.Minute)) {
		account = loginWindow{start: now}
	}
	if account.count >= 5 {
		return account.start.Add(5 * time.Minute).Sub(now)
	}
	l.global.count++
	account.count++
	l.accounts[email] = account
	return 0
}
