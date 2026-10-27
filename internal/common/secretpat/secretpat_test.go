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
