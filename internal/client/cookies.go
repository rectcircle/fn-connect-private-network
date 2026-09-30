package client

import (
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

type cookieSet struct {
	jar     *cookiejar.Jar
	records []Cookie
}

func newCookieSet(records []Cookie) (*cookieSet, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	result := &cookieSet{jar: jar}
	for _, record := range records {
		if record.Name == "" || record.Domain == "" {
			return nil, errors.New("cookie name and domain are required")
		}
		host := strings.TrimPrefix(strings.ToLower(record.Domain), ".")
		scheme := "http"
		if record.Secure {
			scheme = "https"
		}
		origin := &url.URL{Scheme: scheme, Host: host, Path: "/"}
		cookie := record.httpCookie()
		if record.HostOnly {
			cookie.Domain = ""
		}
		result.ApplyResponse(&http.Response{
			Request: &http.Request{URL: origin},
			Header:  http.Header{"Set-Cookie": []string{cookie.String()}},
		})
	}
	return result, nil
}

func (s *cookieSet) Cookies(target *url.URL) []*http.Cookie {
	cookieURL := *target
	switch cookieURL.Scheme {
	case "ws":
		cookieURL.Scheme = "http"
	case "wss":
		cookieURL.Scheme = "https"
	}
	return s.jar.Cookies(&cookieURL)
}

func (s *cookieSet) Header(target *url.URL) string {
	request := &http.Request{Header: make(http.Header), URL: target}
	for _, cookie := range s.Cookies(target) {
		request.AddCookie(cookie)
	}
	return request.Header.Get("Cookie")
}

func (s *cookieSet) ApplyResponse(response *http.Response) bool {
	if response == nil || response.Request == nil {
		return false
	}
	updates := response.Cookies()
	if len(updates) == 0 {
		return false
	}
	s.jar.SetCookies(response.Request.URL, updates)
	now := time.Now()
	for _, update := range updates {
		if !acceptsCookie(update, response.Request.URL) {
			continue
		}
		record := cookieFromHTTP(update, response.Request.URL, now)
		index := -1
		for candidate := range s.records {
			if cookieKey(s.records[candidate]) == cookieKey(record) {
				index = candidate
				break
			}
		}
		expired := update.MaxAge < 0 ||
			(!record.Expires.IsZero() && record.Expires.Before(now))
		switch {
		case expired && index >= 0:
			s.records = append(s.records[:index], s.records[index+1:]...)
		case !expired && index >= 0:
			s.records[index] = record
		case !expired:
			s.records = append(s.records, record)
		}
	}
	return true
}

func (s *cookieSet) Records() []Cookie {
	return append([]Cookie(nil), s.records...)
}

// Let the standard jar validate scope even for deletion/expired cookies.
func acceptsCookie(cookie *http.Cookie, origin *url.URL) bool {
	jar, _ := cookiejar.New(nil)
	probe := *cookie
	probe.MaxAge = 0
	probe.Expires = time.Time{}
	probe.Path = "/"
	probe.Secure = false
	jar.SetCookies(origin, []*http.Cookie{&probe})
	return len(jar.Cookies(origin)) != 0
}

func cookieFromHTTP(cookie *http.Cookie, origin *url.URL, now time.Time) Cookie {
	domain := strings.TrimPrefix(strings.ToLower(cookie.Domain), ".")
	hostOnly := domain == ""
	if hostOnly {
		domain = strings.ToLower(origin.Hostname())
	}
	path := cookie.Path
	if path == "" || path[0] != '/' {
		path = "/"
		if index := strings.LastIndex(origin.Path, "/"); index > 0 {
			path = origin.Path[:index]
		}
	}
	expires := cookie.Expires
	if cookie.MaxAge > 0 {
		expires = now.Add(time.Duration(cookie.MaxAge) * time.Second)
	}
	return Cookie{
		Name:     cookie.Name,
		Value:    cookie.Value,
		Domain:   domain,
		Path:     path,
		Secure:   cookie.Secure,
		HTTPOnly: cookie.HttpOnly,
		HostOnly: hostOnly,
		SameSite: sameSiteName(cookie.SameSite),
		Expires:  expires,
	}
}

func (c Cookie) httpCookie() *http.Cookie {
	return &http.Cookie{
		Name:     c.Name,
		Value:    c.Value,
		Domain:   c.Domain,
		Path:     valueOrDefault(c.Path, "/"),
		Secure:   c.Secure,
		HttpOnly: c.HTTPOnly,
		SameSite: parseSameSite(c.SameSite),
		Expires:  c.Expires,
	}
}

func cookieKey(cookie Cookie) string {
	return cookie.Name + "\x00" +
		strings.TrimPrefix(strings.ToLower(cookie.Domain), ".") + "\x00" +
		cookie.Path
}

func sameSiteName(value http.SameSite) string {
	switch value {
	case http.SameSiteStrictMode:
		return "strict"
	case http.SameSiteLaxMode:
		return "lax"
	case http.SameSiteNoneMode:
		return "none"
	default:
		return ""
	}
}

func parseSameSite(value string) http.SameSite {
	switch strings.ToLower(value) {
	case "strict":
		return http.SameSiteStrictMode
	case "lax":
		return http.SameSiteLaxMode
	case "none":
		return http.SameSiteNoneMode
	default:
		return http.SameSiteDefaultMode
	}
}
