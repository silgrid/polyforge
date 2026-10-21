package gitops

import (
	"errors"
	"testing"
)

func TestIsTransientRemoteFailure(t *testing.T) {
	for msg, want := range map[string]bool{
		"fatal: unable to access 'http://h/x.git/': Failed to connect to h port 80: Connection refused": true,
		"fatal: unable to access 'http://h/x.git/': Could not resolve host: h":                          true,
		"fatal: unable to access 'http://h/x.git/': The requested URL returned error: 502":              true,
		"fatal: the remote end hung up unexpectedly":                                                    true,
		"timed out after 30s: signal: killed":                                                           true,
		"fatal: unable to access 'http://h/x.git/': The requested URL returned error: 403":              false,
		"fatal: Authentication failed for 'http://h/x.git/'":                                            false,
		"fatal: repository 'http://h/x.git/' not found":                                                 false,
		"fatal: not a git repository":                                                                   false,
	} {
		if got := IsTransientRemoteFailure(&gitError{msg: "git ls-remote: " + msg, err: errors.New("exit status 128")}); got != want {
			t.Errorf("%q: transient=%v, want %v", msg, got, want)
		}
	}
	if IsTransientRemoteFailure(nil) {
		t.Error("nil is not a failure")
	}
}
