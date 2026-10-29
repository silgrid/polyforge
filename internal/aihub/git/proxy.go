package git

// The git-http-backend smart-protocol proxy: aihub mounts /git/{wi}.git/…
// and execs the real `git http-backend` CGI. Basic auth: username must be
// the work-item id and the password must be the WI's LIVE session secret
// (reference.md §3.4 fencing-on-use) — a superseded or ended session's secret is
// rejected here (GIT-401).

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
)

// wiRepoSegment pins the repo segment of the git face: the canonical
// remote is /git/<wi_id>.git where the id body matches the imported id
// domain (core's wiIDPattern: "wi_" + identifier body). The single exact
// ".git" suffix is required — the suffix-less, doubled-suffix, empty and
// garbage segment shapes all fail here in one stroke — and no dot can
// appear inside the id, so the TrimSuffix below is exact.
var wiRepoSegment = regexp.MustCompile(`^wi_[A-Za-z0-9_-]+\.git$`)

// HandleGit is the http.Handler for the /git/ prefix.
func (m *Manager) HandleGit(w http.ResponseWriter, r *http.Request) {
	// Path shape after /git/: "<wi_id>.git[/<endpoint>]". Shape first
	// (404): a malformed path names no repo to authenticate against.
	rest := strings.TrimPrefix(r.URL.Path, "/git/")
	rest = strings.TrimPrefix(rest, "/")
	segment := rest
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		segment = rest[:i]
	}
	if !wiRepoSegment.MatchString(segment) {
		writeGitErr(w, api.ErrGitNotFound, "unknown git path %q", r.URL.Path)
		return
	}
	wiID := strings.TrimSuffix(segment, ".git")
	// Traversal never reaches CGI: any dot-segment — or a component
	// embedding ".." — is refused here with a typed 404. Encoded forms
	// (%2e%2e and friends) are already decoded into r.URL.Path by the
	// time the proxy sees them, so they hit this fence too.
	for _, comp := range strings.Split(rest, "/") {
		if comp == "." || strings.Contains(comp, "..") {
			writeGitErr(w, api.ErrGitNotFound, "path traversal rejected: %q", r.URL.Path)
			return
		}
	}
	// Method/target coherence (400) — needs a well-formed path to reason
	// about its endpoint. Smart HTTP has exactly three targets: GET
	// advertises at info/refs, POST drives git-upload-pack and
	// git-receive-pack; anything else has no valid smart-HTTP method and
	// is refused before CGI.
	endpoint := ""
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		endpoint = rest[i+1:]
	}
	switch {
	case endpoint == "info/refs" && r.Method == http.MethodGet,
		endpoint == "git-upload-pack" && r.Method == http.MethodPost,
		endpoint == "git-receive-pack" && r.Method == http.MethodPost:
	default:
		writeGitErr(w, api.ErrGitBadRequest, "method %s is not valid for %q", r.Method, r.URL.Path)
		return
	}

	// Basic auth against the session (reference.md §3.4): the password is the
	// session secret, and only the work item's live session passes.
	user, secret, ok := r.BasicAuth()
	if !ok {
		w.Header().Set("WWW-Authenticate", `Basic realm="polyforge"`)
		writeGitErr(w, api.ErrGitUnauthorized, "git access requires a session-scoped credential")
		return
	}
	if err := m.checker.CheckSessionCredential(wiID, user, secret); err != nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="polyforge"`)
		// The git client only understands the 401; the typed code rides in
		// the response headers for diagnostics.
		ge, isTyped := err.(*api.Error)
		code := api.ErrGitUnauthorized
		detail := err.Error()
		if isTyped {
			code, detail = ge.Code, ge.Detail
		}
		w.Header().Set("X-PF-Git-Error", code)
		writeGitErr(w, code, "%s", detail)
		return
	}

	// Request-body cap, declared half: a Content-Length
	// above the constant cap is refused before any CGI is spawned, so
	// the body is never streamed and no quarantine or partial objects
	// can appear. GIT-400 — the existing bad-request shape, no new code.
	if r.ContentLength > m.MaxGitRequestBodyBytes {
		writeGitErr(w, api.ErrGitBadRequest,
			"git request body %d bytes exceeds the %d-byte limit", r.ContentLength, m.MaxGitRequestBodyBytes)
		return
	}
	// The repo must exist (created at claim time). A clone that races a
	// claim is served a clean 404 rather than an empty CGI 500.
	repoDir := m.RepoDir(wiID)
	if _, err := os.Stat(filepath.Join(repoDir, "HEAD")); err != nil {
		writeGitErr(w, api.ErrGitNotFound, "no repository for %s", wiID)
		return
	}
	// A repo this process has not ensured yet (one created by an earlier
	// build) gets this build's configuration and hook before its first
	// push: the chain rules are always the running hub's.
	if err := m.EnsureBare(wiID); err != nil {
		writeGitErr(w, api.ErrGitInternal, "prepare repository for %s: %v", wiID, err)
		return
	}

	// CGI environment (git http-backend contract). Base is the inherited
	// process environment: http-backend execs git-receive-pack/upload-pack
	// from PATH, and the pre-receive hook (a bash script exec'd by
	// receive-pack) inherits this environment too.
	env := os.Environ()
	env = append(env,
		"GIT_PROJECT_ROOT="+m.Root,
		"GIT_HTTP_EXPORT_ALL=1",
		"PATH_INFO=/"+rest,
		"PATH_TRANSLATED="+filepath.Join(m.Root, rest),
		"REQUEST_METHOD="+r.Method,
		"QUERY_STRING="+r.URL.RawQuery,
		"REMOTE_USER="+wiID,
		"REMOTE_ADDR="+remoteAddr(r),
		"CONTENT_TYPE="+r.Header.Get("Content-Type"),
		"GIT_PF_MAX_BLOB="+strconv.FormatInt(m.MaxBlobBytes, 10),
		"GIT_PF_MAX_TASK_BLOB="+strconv.FormatInt(m.MaxTaskBlobBytes, 10),
	)
	if n := r.Header.Get("Content-Length"); n != "" {
		env = append(env, "CONTENT_LENGTH="+n)
	}
	// Protocol v2 negotiation travels in the Git-Protocol header.
	if gp := r.Header.Get("Git-Protocol"); gp != "" {
		env = append(env, "GIT_PROTOCOL="+gp)
	}
	env = append(env, cgiHeaderEnv(r.Header)...)

	cmd := exec.Command("git", "http-backend")
	cmd.Env = env
	cmd.Dir = m.Root
	stdin, err := cmd.StdinPipe()
	if err != nil {
		writeGitErr(w, api.ErrGitInternal, "%v", err)
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		writeGitErr(w, api.ErrGitInternal, "%v", err)
		return
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		writeGitErr(w, api.ErrGitInternal, "start http-backend: %v", err)
		return
	}
	// Body → CGI stdin, capped: the wrapper passes at
	// most cap bytes and probes one more to catch a chunked/undeclared
	// over-limit body — the CGI then sees a truncated pack, rejects it
	// and cleans its own quarantine up (no process kill, no residue).
	// The copy's verdict (over / not over) is AWAITED below before any
	// status line is written: git http-backend writes its CGI response
	// headers BEFORE run_service reads stdin (verified on git 2.43: a
	// receive-pack POST's headers arrive while the pack is still in
	// flight), so the headers' arrival decides nothing — a non-blocking
	// probe after the header parse would answer a chunked over-cap body
	// with the CGI's 200.
	capped := &overCapReader{rc: r.Body, limit: m.MaxGitRequestBodyBytes}
	verdict := make(chan bool, 1)
	go func() {
		_, _ = io.Copy(stdin, capped)
		stdin.Close()
		verdict <- capped.over
	}()

	// Await the verdict BEFORE reading the CGI response at all. This
	// cannot deadlock: http-backend writes only its small header block
	// before it consumes stdin (far under any pipe buffer), and it
	// writes its response body only after stdin is fully read — the
	// CGI can never block on a full stdout pipe while the copy still
	// holds unread stdin. The copy ends on the body's EOF or a
	// broken CGI stdin (EPIPE) either way, so the verdict always
	// arrives; TestOverCapChunkedBodyRefused exercises exactly the
	// sensitive ordering (CGI headers out, stdin still open, proxy
	// waiting).
	if over := <-verdict; over {
		// Drain the body's unread remainder first, bounded: closing
		// with unread request bytes would RST the connection and could
		// clobber the refusal the client is about to read. A caller
		// that keeps streaming past this bound loses the response leg,
		// never the refusal itself.
		_, _ = io.CopyN(io.Discard, r.Body, 1<<20)
		// The CGI saw exactly cap bytes then EOF — a truncated pack it
		// rejects and cleans up itself. Discard its output, reap it,
		// answer the refusal (GIT-400) instead of forwarding the CGI's
		// status.
		_, _ = io.Copy(io.Discard, stdout)
		_ = cmd.Wait()
		writeGitErr(w, api.ErrGitBadRequest,
			"git request body exceeds the %d-byte limit", m.MaxGitRequestBodyBytes)
		return
	}

	// Parse the CGI response: headers, then body.
	br := bufio.NewReader(stdout)
	status := http.StatusOK
	hdr := http.Header{}
	for {
		line, rerr := br.ReadString('\n')
		if rerr != nil {
			writeGitErr(w, api.ErrGitInternal, "http-backend died before headers")
			_, _ = cmd.Process.Wait()
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		i := strings.IndexByte(line, ':')
		if i < 0 {
			continue
		}
		name, value := strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:])
		switch strings.ToLower(name) {
		case "status":
			if fields := strings.Fields(value); len(fields) > 0 {
				if code, cerr := strconv.Atoi(fields[0]); cerr == nil {
					status = code
				}
			}
		default:
			hdr.Add(name, value)
		}
	}
	for k, vs := range hdr {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(status)
	_, _ = io.Copy(w, br)
	_ = cmd.Wait()
}

// overCapReader passes at most limit bytes of the underlying body
// through to the CGI and decides, with one probe read past the limit,
// whether the request carries more: a body that crosses the limit
// yields EOF after exactly limit bytes (the CGI gets a truncated pack
// it rejects and cleans up itself — no process kill, so no SIGKILL
// quarantine residue) and sets over; a body that ends at or under the
// limit passes through untouched, probe and all. The proxy awaits the
// copy's over flag before answering anything — the CGI's response
// headers say nothing about the body: http-backend writes them before
// it reads stdin.
type overCapReader struct {
	rc    io.ReadCloser
	limit int64
	over  bool
}

func (r *overCapReader) Read(p []byte) (int, error) {
	if r.limit <= 0 {
		// One probe byte past the limit: data there means the body is
		// over the cap and the connection is refused, not streamed.
		var b [1]byte
		n, err := r.rc.Read(b[:])
		if n > 0 {
			r.over = true
			return 0, io.EOF
		}
		return 0, err // an honest EOF at exactly the limit
	}
	if int64(len(p)) > r.limit {
		p = p[:r.limit]
	}
	n, err := r.rc.Read(p)
	r.limit -= int64(n)
	return n, err
}

func remoteAddr(r *http.Request) string {
	if i := strings.LastIndexByte(r.RemoteAddr, ':'); i > 0 {
		return r.RemoteAddr[:i]
	}
	return r.RemoteAddr
}

// writeGitErr renders the typed envelope for git-surface failures that
// never reach CGI (auth, unknown paths, backend startup). The git client
// itself only reacts to the HTTP status.
func writeGitErr(w http.ResponseWriter, code, format string, args ...any) {
	e := &api.Error{Code: code, Detail: fmt.Sprintf(format, args...)}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.HTTPStatus())
	// Deliberately small writes; git clients print this on failure.
	_, _ = fmt.Fprintf(w, `{"code":%q,"detail":%q}`, e.Code, e.Detail)
}

// cgiHeaderEnv maps request headers to CGI HTTP_* variables (http-backend
// reads HTTP_ACCEPT etc.). Credential headers are dropped: the caller is
// already authenticated (REMOTE_USER carries the WI), and http-backend,
// receive-pack and the pre-receive hook all inherit this environment —
// the session secret stays out of it (reference.md §3.1).
func cgiHeaderEnv(h http.Header) []string {
	var env []string
	for k, v := range h {
		switch k {
		case "Content-Type", "Content-Length", "Authorization", "Proxy-Authorization", "Cookie":
			continue
		}
		if strings.HasPrefix(k, "Git-") {
			continue
		}
		name := "HTTP_" + strings.ToUpper(strings.ReplaceAll(k, "-", "_"))
		for _, hv := range v {
			env = append(env, name+"="+hv)
		}
	}
	return env
}
