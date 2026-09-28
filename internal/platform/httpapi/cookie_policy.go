package httpapi

import (
	"context"
	"net/http"
	"time"
)

type browserCookiePolicy struct {
	secure      bool
	sessionName string
	csrfName    string
}

type browserCookiePolicyKey struct{}

func newBrowserCookiePolicy(secure bool) browserCookiePolicy {
	policy := browserCookiePolicy{secure: secure, sessionName: sessionCookieName, csrfName: csrfCookieName}
	if !secure {
		policy.sessionName = "control_session"
		policy.csrfName = "control_csrf"
	}
	return policy
}

func withBrowserCookiePolicy(policy browserCookiePolicy, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), browserCookiePolicyKey{}, policy)))
	})
}

func browserCookies(r *http.Request) browserCookiePolicy {
	if policy, ok := r.Context().Value(browserCookiePolicyKey{}).(browserCookiePolicy); ok {
		return policy
	}
	return newBrowserCookiePolicy(true)
}

func identitySessionCookie(r *http.Request) (*http.Cookie, error) {
	return r.Cookie(browserCookies(r).sessionName)
}

func (policy browserCookiePolicy) cookies(session, csrf string) []*http.Cookie {
	return []*http.Cookie{
		{Name: policy.sessionName, Value: session, Path: "/", Secure: policy.secure, HttpOnly: true, SameSite: http.SameSiteLaxMode},
		{Name: policy.csrfName, Value: csrf, Path: "/", Secure: policy.secure, SameSite: http.SameSiteLaxMode},
	}
}

func (policy browserCookiePolicy) set(w http.ResponseWriter, session, csrf string, expires time.Time) {
	policy.clearOtherMode(w)
	for _, cookie := range policy.cookies(session, csrf) {
		cookie.Expires = expires
		http.SetCookie(w, cookie)
	}
}

func (policy browserCookiePolicy) clearOtherMode(w http.ResponseWriter) {
	newBrowserCookiePolicy(!policy.secure).clear(w)
}

func (policy browserCookiePolicy) clear(w http.ResponseWriter) {
	for _, cookie := range policy.cookies("", "") {
		cookie.MaxAge = -1
		http.SetCookie(w, cookie)
	}
}
