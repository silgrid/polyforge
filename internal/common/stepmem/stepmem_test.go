package stepmem

import (
	"strings"
	"testing"
)

func TestSecretValueDefect(t *testing.T) {
	for _, ok := range []string{
		"TestShutdownLateBoundRecoveryStaysRecovering failed under load",
		"see internal/polyforge/daemon/TestStepRoutesResolveTheSpawnedStepAndRefusesForgedWorkItems",
		"the key lives in ~/.config/polyforge/cli.toml (api_key=${PF_API_KEY})",
		"commit 4493407e2f0c9a1b5d6e7f8a9b0c1d2e3f4a5b6c and uuid 3f2b1c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d",
		"secret: os.Getenv(\"PF_API_KEY\")",
		"very_long_snake_case_identifier_that_is_still_not_a_secret_value",
	} {
		if why := SecretValueDefect(ok); why != "" {
			t.Errorf("%q refused: %s", ok, why)
		}
	}
	for _, bad := range []string{
		"key sk-ant-api03-Zx9Qw2Er5Ty8Ui1Op4As7Df0Gh3Jk6Lz",
		"token ghp_abcdefghijklmnopqrstuvwxyz0123",
		"jwt eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
		"-----BEGIN OPENSSH PRIVATE KEY-----",
		"AKIAIOSFODNN7EXAMPLE",
		"blob Zm9vYmFyYmF6cXV4MTIzNDU2Nzg5MEFCQ0RFRkdISUpLTE1OT1BRUlNUVVZX",
	} {
		if SecretValueDefect(bad) == "" {
			t.Errorf("%q passed", bad)
		}
	}
}

func TestCheck(t *testing.T) {
	if why := Check("experience.wrap", "proj#1: done", []string{"policy-proposal"}, "body"); why != "" {
		t.Fatal(why)
	}
	for want, args := range map[string][4]string{
		"only its experience.wrap": {"experience.debug", "t", "", "b"},
		"title is required":        {WrapType, " ", "", "b"},
		"one line":                 {WrapType, "a\nb", "", "b"},
		"body is empty":            {WrapType, "t", "", " "},
		"at most 2048":             {WrapType, "t", "", strings.Repeat("x", 2049)},
		"not a plain name":         {WrapType, "t", "bad tag", "b"},
	} {
		var tags []string
		if args[2] != "" {
			tags = []string{args[2]}
		}
		if why := Check(args[0], args[1], tags, args[3]); !strings.Contains(why, want) {
			t.Errorf("%v: %q, want %q", args, why, want)
		}
	}
}
