package server

// web_shell.go: the console's document and asset serving. The SPA
// builds into webui/dist and is embedded in the aihub binary; every
// authenticated document path answers the ONE shell (dist/index.html,
// 200 — the client router resolves deep links), and /web/assets/...
// answers the embedded assets by their exact name. A missing asset is
// 404 bytes, never the shell; no redirect is ever emitted.

import (
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// distFS is the minimal file-tree face the console serving needs.
type distFS interface {
	Open(name string) (fs.File, error)
}

// webContentTypes maps the console's asset extensions to their exact
// content types (hashed names keep this a tiny, closed set).
var webContentTypes = map[string]string{
	".js":    "text/javascript; charset=utf-8",
	".mjs":   "text/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".svg":   "image/svg+xml",
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".woff":  "font/woff",
	".woff2": "font/woff2",
	".map":   "application/json",
	".txt":   "text/plain; charset=utf-8",
	".html":  "text/html; charset=utf-8",
}

// serveWebShell answers the one embedded shell (dist/index.html) with
// 200. Every authenticated document path shares it: the client parses
// the path and renders the view — or the one not-found view when the
// private JSON face answers 404.
func (s *Server) serveWebShell(w http.ResponseWriter, r *http.Request) {
	s.writeWebDistFile(w, r, "index.html", http.StatusOK)
}

// serveWebAsset answers one embedded asset by its exact name under
// dist/assets/. A missing or non-asset name is the static 404 body —
// never the shell — and an asset never shadows the api face.
func (s *Server) serveWebAsset(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) != 1 || rest[0] == "" || strings.Contains(rest[0], "/") {
		writeWebNotFound(w, r)
		return
	}
	s.writeWebDistFile(w, r, path.Join("assets", rest[0]), http.StatusOK)
}

// writeWebDistFile writes one embedded dist file. A file the embedded
// tree does not carry answers the static 404 body (a shell that is
// absent on an unbuilt tree is the same answer as a missing asset).
func (s *Server) writeWebDistFile(w http.ResponseWriter, r *http.Request, name string, status int) {
	dist := s.webDistFS()
	if dist == nil {
		writeWebNotFound(w, r)
		return
	}
	f, err := dist.Open(name)
	if err != nil {
		writeWebNotFound(w, r)
		return
	}
	defer f.Close()
	body, err := io.ReadAll(f)
	if err != nil {
		writeWebNotFound(w, r)
		return
	}
	writeWebBody(w, r, status, webContentType(name), body)
}

// webContentType answers the file's content type by extension;
// anything the closed map lacks is the opaque application/octet-stream,
// so a browser never sniffs a console asset into a scriptable type.
func webContentType(name string) string {
	if ct, ok := webContentTypes[strings.ToLower(path.Ext(name))]; ok {
		return ct
	}
	return "application/octet-stream"
}
