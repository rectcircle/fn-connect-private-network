package client

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestCookieSetHonorsHostOnlyAndPathBoundaries(t *testing.T) {
	set, err := newCookieSet([]Cookie{
		{
			Name:     "host",
			Value:    "one",
			Domain:   "home.fnos.net",
			Path:     "/app/fncpn",
			Secure:   true,
			HostOnly: true,
		},
		{
			Name:   "domain",
			Value:  "two",
			Domain: ".fnos.net",
			Path:   "/",
			Secure: true,
		},
	})
	if err != nil {
		t.Fatalf("new cookie set: %v", err)
	}
	target, _ := url.Parse("https://home.fnos.net/app/fncpn/api/v1/bootstrap")
	header := set.Header(target)
	if !strings.Contains(header, "host=one") || !strings.Contains(header, "domain=two") {
		t.Fatalf("cookie header = %q", header)
	}
	sibling, _ := url.Parse("https://other.fnos.net/app/fncpn/api/v1/bootstrap")
	header = set.Header(sibling)
	if strings.Contains(header, "host=one") || !strings.Contains(header, "domain=two") {
		t.Fatalf("sibling cookie header = %q", header)
	}
	wrongPath, _ := url.Parse("https://home.fnos.net/app/fncpnext")
	if header = set.Header(wrongPath); strings.Contains(header, "host=one") {
		t.Fatalf("path-prefix cookie leaked: %q", header)
	}
}

func TestCookiePersistenceMatchesJar(t *testing.T) {
	origin, _ := url.Parse("https://home.fnos.net/app/fncpn/api/bootstrap")
	for _, test := range []struct {
		name    string
		updates []string
		target  string
		want    string
	}{
		{"delete_without_samesite", []string{
			"session=old; Path=/; SameSite=Lax", "session=; Path=/; Max-Age=0",
		}, origin.String(), ""},
		{"case_sensitive_names", []string{"Session=A; Path=/", "session=b; Path=/"}, origin.String(), "Session=A; session=b"},
		{"default_path", []string{"session=secret"}, "https://home.fnos.net/another-app", ""},
		{"invalid_path_defaults", []string{"session=secret; Path=relative"}, origin.String(), "session=secret"},
		{"canonical_domain", []string{
			"session=old; Domain=.FNOS.NET; Path=/", "session=new; Domain=fnos.net; Path=/",
		}, origin.String(), "session=new"},
		{"reject_foreign_domain", []string{"session=secret; Domain=evil.example"}, origin.String(), ""},
		{"maxage_overrides_expires", []string{
			"session=valid; Max-Age=600; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
		}, origin.String(), "session=valid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			set, err := newCookieSet(nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, update := range test.updates {
				set.ApplyResponse(&http.Response{
					Request: &http.Request{URL: origin},
					Header: http.Header{"Set-Cookie": []string{update}},
				})
			}
			target, _ := url.Parse(test.target)
			before := set.Header(target)
			restored, err := newCookieSet(set.Records())
			if err != nil || before != test.want || restored.Header(target) != test.want {
				t.Fatalf("before=%q after=%q want=%q err=%v", before, restored.Header(target), test.want, err)
			}
		})
	}
}

func TestCookieMaxAgePersistsAnAbsoluteExpiry(t *testing.T) {
	origin, _ := url.Parse("https://home.fnos.net/app/fncpn")
	set, _ := newCookieSet(nil)
	before := time.Now()
	set.ApplyResponse(&http.Response{
		Request: &http.Request{URL: origin},
		Header: http.Header{"Set-Cookie": []string{"session=secret; Max-Age=60"}},
	})
	records := set.Records()
	if len(records) != 1 || records[0].Expires.Before(before.Add(59*time.Second)) ||
		records[0].Expires.After(time.Now().Add(time.Minute)) {
		t.Fatalf("absolute expiry missing: %+v", records)
	}
	records[0].Expires = time.Now().Add(-time.Second)
	restored, err := newCookieSet(records)
	if err != nil || restored.Header(origin) != "" || len(restored.Records()) != 0 {
		t.Fatalf("expired cookie restored: %+v err=%v", restored.Records(), err)
	}
}
