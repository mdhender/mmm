// Copyright (c) 2026 Michael D Henderson.

package web

import (
	"net/http"
	"net/url"
	"strings"
)

// controlRefused is what a control request that did not come from this program's
// own pages is told. It is a page rather than a bare status because the reader,
// if there is one, deserves the same treatment as any other failure (RG-4).
const (
	controlRefusedHeading = "That request did not come from the checkbook"
	controlRefusedDetail  = "Backing up, closing, opening and quitting act on the program and on your file rather than on a record, so they are only accepted from a page this program served. Nothing was done."
	controlRefusedStep    = "Use the links in the checkbook's own window. If you reached this from a link on another site, close the tab: that site was trying to act on your records."
)

// control guards the requests whose effect is on the process or on the file
// rather than on a record: Back up now, Close, Open, and Quit.
//
// This is not the authentication, session, or CSRF machinery PL-7 forbids, and
// it is worth being clear about the difference. There is no token, no state, and
// no principal: it is two headers the browser fills in by itself, read to answer
// one question -- did this request come from a page this program served. The
// register still has no notion of who is asking, because there is only ever one
// household and it is at this machine.
//
// The reason it is needed at all is that these four are the first actions whose
// effect is not a record. Any page on the internet can post a form to
// 127.0.0.1:8842; until now the worst that could do was write a transaction a
// reader would see. Quitting the program or closing their checkbook is different
// enough to be worth the two lines.
//
//   - GET is never a control route, so nothing here can be fired by a link, a
//     prefetch, or an image.
//   - Sec-Fetch-Site, when present, must say same-origin. Browsers always send
//     it; curl and httptest send neither header, so the terminal and the tests
//     are unaffected.
//   - Origin, when present, must be this server's own.
func (s *Server) control(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			s.log.Warn("control request refused",
				"path", r.URL.Path,
				"origin", r.Header.Get("Origin"),
				"sec_fetch_site", r.Header.Get("Sec-Fetch-Site"))
			s.fail(w, r, http.StatusForbidden, nil,
				controlRefusedHeading, controlRefusedDetail, controlRefusedStep)
			return
		}
		h(w, r)
	}
}

// sameOrigin reports whether the request came from a page this server served.
//
// Both headers are optional on purpose. Absence means the request did not come
// from a browser at all -- a script, a terminal, or a test -- and those already
// have the run of the machine; there is nothing for this check to protect them
// from. It is a browser being pointed at loopback by somebody else's page that
// this refuses.
func sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" || origin == "null" {
		return origin == ""
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	// Host, not URL.Host: the request line's authority is what the browser was
	// pointed at, and it is what the origin has to match.
	return strings.EqualFold(u.Host, r.Host)
}
