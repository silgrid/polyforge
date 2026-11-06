package secretpat

import (
	"strings"
	"testing"
)

func TestScanCatchesRequiredShapes(t *testing.T) {
	for _, text := range []string{
		"key pf_k1_ABCDEFGHIJKLMNOP",
		strings.Repeat("a", 64),
		"-----BEGIN RSA PRIVATE KEY-----\nMIIE\n-----END RSA PRIVATE KEY-----",
		"sk-abcdefghijklmnopqrstuvwxyz",
		"key key_ABCDEFGHIJKL",
	} {
		if _, hit := Scan(text); !hit {
			t.Fatalf("secret shape not caught: %q", text)
		}
	}
	for _, clean := range []string{"w11 clean", "short deadbeef", "-----BEGIN PUBLIC KEY-----", "please review the plan", "monkey_business123 is ordinary prose"} {
		if name, hit := Scan(clean); hit {
			t.Fatalf("ordinary text tripped %s: %q", name, clean)
		}
	}
}

// Mask: every credential shape is replaced, the text
// around it kept; a clean text passes unchanged.
func TestMaskReplacesCredentialShapes(t *testing.T) {
	gh := "ghp_" + strings.Repeat("Zz9", 12)
	in := "push " + gh + " via https://deploy:pa55-word@proxy.example:3128/x and git://tok@h/r\n" +
		"DEPLOY_API_KEY=k-123 GITHUB_TOKEN=t-456 db_password=p-789 --password  hunter-9 ok"
	got := Mask(in)
	for _, secret := range []string{gh, "deploy:pa55-word", "tok@", "k-123", "t-456", "p-789", "hunter-9"} {
		if strings.Contains(got, secret) {
			t.Errorf("Mask left %q:\n%s", secret, got)
		}
	}
	for _, kept := range []string{"push [masked github_token]", "https://***@proxy.example:3128/x", "git://***@h/r", "DEPLOY_API_KEY=***", "--password  *** ok"} {
		if !strings.Contains(got, kept) {
			t.Errorf("Mask output lacks %q:\n%s", kept, got)
		}
	}
	clean := "ok 12 tests passed in 3.2s (see https://example.com/run/7)"
	if Mask(clean) != clean {
		t.Errorf("a clean text changed: %q", Mask(clean))
	}
}
