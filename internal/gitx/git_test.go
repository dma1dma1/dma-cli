package gitx

import "testing"

func TestParseRemote(t *testing.T) {
	cases := map[string]string{
		"git@github.com:owner/name.git":         "owner/name",
		"git@github.com:owner/name":             "owner/name",
		"https://github.com/owner/name.git":     "owner/name",
		"https://github.com/owner/name":         "owner/name",
		"ssh://git@github.com/owner/name.git":   "owner/name",
		"git@ssh.github.com:443/owner/name.git": "owner/name",
	}
	for in, want := range cases {
		got, err := ParseRemote(in)
		if err != nil {
			t.Errorf("ParseRemote(%q) errored: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseRemote(%q) = %q, want %q", in, got, want)
		}
	}

	if _, err := ParseRemote(""); err == nil {
		t.Error("empty remote should error")
	}
}

func TestParseNumstat(t *testing.T) {
	out := "10\t2\tfile.go\n5\t0\tother.go\n-\t-\timage.png\n"
	added, removed := parseNumstat(out)
	if added != 15 || removed != 2 {
		t.Fatalf("parseNumstat = +%d -%d, want +15 -2", added, removed)
	}
}

func TestParseNumstatIgnoresGarbage(t *testing.T) {
	added, removed := parseNumstat("not a numstat line\n\n")
	if added != 0 || removed != 0 {
		t.Fatalf("parseNumstat = +%d -%d, want zero", added, removed)
	}
}
