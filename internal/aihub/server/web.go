package server

// web.go: the /web/ console wrapper — the aihub server's read-only web
// face. One wrapper owns the whole response of every /web/ route in a
// fixed order: the strict security header set first, then Basic
// authentication (401 + one static zero-information body; the username
// is ignored, the password is resolved through the one api-key query
// the Bearer face uses, so every key kind meets the same read gates),
// then the GET/HEAD method gate (405 + Allow) — so an unauthenticated
// POST answers the 401 challenge and an authenticated non-GET/HEAD
// request does no page or data work. Nothing is persisted: no cookie,
// no session, every request re-authenticates.
//
// The wrapper is reached from Server.ServeHTTP, which intercepts every
// raw path under /web BEFORE http.ServeMux can canonicalise it: no
// path-cleanup 301 can bypass the header write (the viewer's N5
// defect), and the face emits no redirects at all.
//
// Reachability (AGENTS.md): a human at a browser holding an api key.
// A workflow step or task agent holds no aihub key at all, and the
// daemon proxies no /web/ route (its route set 404s /web/...), so no
// step process reaches this face.

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// webCSP is the strict policy every /web/ response carries: the console
// is a same-origin SPA whose shell references only hashed external
// scripts and styles — no inline script or style exists — so
// 'self' alone permits everything the shell needs and nothing else.
const webCSP = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"

// The one static body per error class, byte-identical within each class
// (unknown and unreadable are the same answer; no existence leaks). The
// 401 body is the #103 guide, restated as one static JSON object: it
// says the password is an aihub api key and that an administrator
// issues one — and nothing else, for missing and wrong credentials
// alike.
const (
	webBodyAuth       = "{\"error\":\"authentication required\",\"guide\":\"the password is an aihub api key, issued by an administrator\"}\n"
	webBodyNotFound   = "{\"error\":\"not found\"}\n"
	webBodyMethod     = "{\"error\":\"method not allowed\"}\n"
	webBodyBadRequest = "{\"error\":\"bad request\"}\n"
)

// setWebSecurityHeaders applies the strict header set to EVERY /web/
// response — 200 documents, assets and JSON, and the 401/404/405/400
// answers alike — before any branch runs.
func setWebSecurityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Security-Policy", webCSP)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Robots-Tag", "noindex")
}

// writeWebBody writes one complete, buffered body: Content-Length is
// set for GET and HEAD alike, and a HEAD writes no body — so its status
// and headers match GET's. A half-written response can never reach the
// wire.
func writeWebBody(w http.ResponseWriter, r *http.Request, status int, contentType string, body []byte) {
	h := w.Header()
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// writeWebNotFound answers the one static 404 body — the same status,
// headers and bytes for unknown and unreadable objects alike.
func writeWebNotFound(w http.ResponseWriter, r *http.Request) {
	writeWebBody(w, r, http.StatusNotFound, "application/json", []byte(webBodyNotFound))
}

// writeWebBadRequest answers the one static 400 body (a malformed
// cursor, an unknown status filter, a missing parameter).
func writeWebBadRequest(w http.ResponseWriter, r *http.Request) {
	writeWebBody(w, r, http.StatusBadRequest, "application/json", []byte(webBodyBadRequest))
}

// webAuthenticate resolves the console's Basic credential. A missing,
// malformed or wrong credential — and any non-Basic Authorization
// header, Bearer included — answers the same 401, the same
// WWW-Authenticate challenge and the same static bytes; no credential
// material is echoed.
func (s *Server) webAuthenticate(w http.ResponseWriter, r *http.Request) *identity {
	var idn *identity
	if _, password, ok := r.BasicAuth(); ok {
		var err error
		if idn, err = s.keyIdentity(password); err != nil {
			idn = nil
		}
	}
	if idn == nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="aihub console", charset="UTF-8"`)
		writeWebBody(w, r, http.StatusUnauthorized, "application/json", []byte(webBodyAuth))
		return nil
	}
	return idn
}

// web is the /web/ subtree entry: headers → authentication → method
// gate → route. Server.ServeHTTP hands it every raw path under /web
// (never the cleaned one).
func (s *Server) web(w http.ResponseWriter, r *http.Request) {
	setWebSecurityHeaders(w)
	idn := s.webAuthenticate(w, r)
	if idn == nil {
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeWebBody(w, r, http.StatusMethodNotAllowed, "application/json", []byte(webBodyMethod))
		return
	}
	segments := webSegments(r)
	switch {
	case len(segments) == 0:
		s.serveWebShell(w, r)
	case segments[0] == "api":
		s.webAPI(w, r, idn, segments[1:])
	case segments[0] == "assets":
		s.serveWebAsset(w, r, segments[1:])
	default:
		// Every other authenticated path is a client document route:
		// the one shell, so deep links resolve client-side.
		s.serveWebShell(w, r)
	}
}

// webSegments answers the decoded path segments under /web: each raw
// segment of the ESCAPED path is PathUnescape'd on its own (the server
// half of the link-escaping fix — a project or object name carrying
// ?, #, /, %, space or non-ASCII resolves to the exact object).
func webSegments(r *http.Request) []string {
	rest := strings.Trim(strings.TrimPrefix(r.URL.EscapedPath(), "/web"), "/")
	if rest == "" {
		return nil
	}
	parts := strings.Split(rest, "/")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		seg, err := url.PathUnescape(p)
		if err != nil {
			seg = p
		}
		out = append(out, seg)
	}
	return out
}

// webQuery answers the URL query parameters (already decoded values).
func webQuery(r *http.Request) url.Values { return r.URL.Query() }

// webDistFS answers the embedded console tree (webui/dist), resolved
// once at construction (New): no first-request write, no race.
func (s *Server) webDistFS() distFS {
	return s.webDist
}
