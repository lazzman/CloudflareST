package management

import (
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type sessionRecord struct {
	ID              string    `json:"id"`
	CSRF            string    `json:"csrf"`
	PasswordVersion string    `json:"password_version"`
	ExpiresAt       time.Time `json:"expires_at"`
}

type loginLimit struct {
	Start       time.Time   `json:"start"`
	Count       int         `json:"count"`
	LockedUntil time.Time   `json:"locked_until"`
	Failures    []time.Time `json:"failures"`
}

func (service *Service) session(request *http.Request) (sessionRecord, error) {
	cookie, err := request.Cookie("cfst_session")
	if err != nil {
		return sessionRecord{}, err
	}
	session, err := get[sessionRecord](service.db, "sessions", service.digest([]byte(cookie.Value)))
	if err != nil {
		return sessionRecord{}, err
	}
	if !service.now().Before(session.ExpiresAt) || session.PasswordVersion != service.passwordVersion {
		return sessionRecord{}, errors.New("会话已失效")
	}
	return session, nil
}

func sameOrigin(request *http.Request) bool {
	if site := request.Header.Get("Sec-Fetch-Site"); site == "cross-site" {
		return false
	}
	if origin := request.Header.Get("Origin"); origin != "" {
		parsed, err := url.Parse(origin)
		if err != nil || !strings.EqualFold(parsed.Host, request.Host) || parsed.Scheme != "http" && parsed.Scheme != "https" {
			return false
		}
	}
	return true
}

func validCSRF(request *http.Request, token string) bool {
	supplied := request.Header.Get("X-CSRF-Token")
	return token != "" && subtle.ConstantTimeCompare([]byte(supplied), []byte(token)) == 1 && sameOrigin(request)
}

func (service *Service) handleSession(response http.ResponseWriter, request *http.Request) {
	service.mu.Lock()
	defer service.mu.Unlock()
	session, sessionErr := service.session(request)
	if request.Method == http.MethodGet {
		if sessionErr == nil {
			writeJSON(response, 200, map[string]any{"authenticated": true, "csrf_token": session.CSRF, "expires_at": session.ExpiresAt})
			return
		}
		token := randomID()
		http.SetCookie(response, &http.Cookie{Name: "cfst_login_csrf", Value: token, Path: "/api/v1/session", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: service.secureCookie, MaxAge: 3600})
		writeJSON(response, 200, map[string]any{"authenticated": false, "csrf_token": token})
		return
	}
	if request.Method == http.MethodDelete {
		if !sameOrigin(request) {
			writeError(response, &apiError{403, "csrf_invalid", "跨站退出请求已拒绝"})
			return
		}
		if sessionErr != nil {
			http.SetCookie(response, &http.Cookie{Name: "cfst_session", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: service.secureCookie, MaxAge: -1})
			writeJSON(response, 200, map[string]bool{"authenticated": false})
			return
		}
		if !validCSRF(request, session.CSRF) {
			writeError(response, &apiError{403, "csrf_invalid", "会话校验失败，请重新读取页面后再操作"})
			return
		}
		if err := remove(service.db, "sessions", session.ID); err != nil {
			writeError(response, err)
			return
		}
		http.SetCookie(response, &http.Cookie{Name: "cfst_session", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: service.secureCookie, MaxAge: -1})
		writeJSON(response, 200, map[string]bool{"authenticated": false})
		return
	}
	if request.Method != http.MethodPost {
		writeError(response, &apiError{405, "method_not_allowed", "不支持的请求方式"})
		return
	}
	csrf, err := request.Cookie("cfst_login_csrf")
	if err != nil || !validCSRF(request, csrf.Value) {
		writeError(response, &apiError{403, "csrf_invalid", "登录校验已失效，请刷新登录页后再试"})
		return
	}
	var input struct {
		Password string `json:"password"`
	}
	if err = decodeJSON(response, request, &input); err != nil {
		writeError(response, err)
		return
	}
	address := service.loginSource(request)
	limitID := service.digest([]byte("login:" + address))
	limit, err := get[loginLimit](service.db, "login_limits", limitID)
	if err != nil && !errors.Is(err, errNotFound) {
		writeError(response, err)
		return
	}
	now := service.now()
	if now.Before(limit.LockedUntil) {
		seconds := int((limit.LockedUntil.Sub(now) + time.Second - 1) / time.Second)
		response.Header().Set("Retry-After", strconv.Itoa(seconds))
		writeError(response, &apiError{429, "login_cooldown", "该来源登录失败过多，请等待" + strconv.Itoa(seconds) + "秒后再试；其他来源不受影响"})
		return
	}
	recent := make([]time.Time, 0, 5)
	for _, failedAt := range limit.Failures {
		if failedAt.After(now.Add(-15 * time.Minute)) {
			recent = append(recent, failedAt)
		}
	}
	limit.Failures = recent
	if bcrypt.CompareHashAndPassword(service.passwordHash, []byte(input.Password)) != nil {
		limit.Failures = append(limit.Failures, now)
		limit.Count = len(limit.Failures)
		if limit.Count >= 5 {
			limit.LockedUntil = now.Add(15 * time.Minute)
		}
		if err = put(service.db, "login_limits", limitID, limit); err != nil {
			writeError(response, err)
			return
		}
		writeError(response, &apiError{401, "invalid_password", "管理密码不正确"})
		return
	}
	if err = put(service.db, "login_limits", limitID, limit); err != nil {
		writeError(response, err)
		return
	}
	token := randomID()
	session = sessionRecord{ID: service.digest([]byte(token)), CSRF: randomID(), PasswordVersion: service.passwordVersion, ExpiresAt: now.Add(7 * 24 * time.Hour)}
	if err = put(service.db, "sessions", session.ID, session); err != nil {
		writeError(response, err)
		return
	}
	http.SetCookie(response, &http.Cookie{Name: "cfst_session", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: service.secureCookie, MaxAge: 7 * 24 * 3600, Expires: session.ExpiresAt})
	writeJSON(response, 200, map[string]any{"authenticated": true, "csrf_token": session.CSRF})
}

func (service *Service) loginSource(request *http.Request) string {
	address, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		address = request.RemoteAddr
	}
	peer, err := netip.ParseAddr(address)
	if err != nil {
		return address
	}
	peer = peer.Unmap()
	trusted := func(ip netip.Addr) bool {
		for _, prefix := range service.trustedProxies {
			if prefix.Contains(ip) {
				return true
			}
		}
		return false
	}
	if !trusted(peer) {
		return peer.String()
	}
	forwarded := request.Header.Get("X-Forwarded-For")
	if forwarded == "" {
		forwarded = request.Header.Get("X-Real-IP")
	}
	if len(forwarded) > 2048 {
		return peer.String()
	}
	chain := strings.Split(forwarded, ",")
	if len(chain) > 32 {
		return peer.String()
	}
	parsed := make([]netip.Addr, 0, len(chain))
	for _, raw := range chain {
		ip, err := netip.ParseAddr(strings.TrimSpace(raw))
		if err != nil || ip.Zone() != "" {
			return peer.String()
		}
		parsed = append(parsed, ip.Unmap())
	}
	current := peer
	for index := len(parsed) - 1; index >= 0; index-- {
		if !trusted(current) {
			break
		}
		current = parsed[index]
	}
	return current.String()
}
