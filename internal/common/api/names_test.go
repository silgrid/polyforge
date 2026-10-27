package api

import "testing"

// PathName builds a path/ref component only from a well-formed slug; any
// other slug (or none) falls back to the id.
func TestPathName(t *testing.T) {
	for _, c := range []struct{ slug, want string }{
		{"polyforge#140", "polyforge-140"},
		{"pi-web_2#7", "pi-web_2-7"},
		{"", "wi_x"},
		{"polyforge#", "wi_x"},
		{"../etc#1", "wi_x"},
		{"Polyforge#1", "wi_x"},
		{"a#1#2", "wi_x"},
		{"a b#1", "wi_x"},
	} {
		if got := PathName(c.slug, "wi_x"); got != c.want {
			t.Errorf("PathName(%q) = %q, want %q", c.slug, got, c.want)
		}
	}
}
