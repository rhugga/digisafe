package app

import (
	"os"
	"regexp"
	"testing"
)

// version.sh is the source of truth for the version; Version is a copy of it that
// `make bump-version` keeps in step. This catches a change to one but not the other.
func TestVersionMatchesVersionSh(t *testing.T) {
	b, err := os.ReadFile("../../version.sh")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^VERSION=(\S+)$`).FindSubmatch(b)
	if m == nil {
		t.Fatal("version.sh has no VERSION= line")
	}
	if got := string(m[1]); got != Version {
		t.Errorf("version.sh says %s, app.Version is %s: run `make bump-version TO=<version>`", got, Version)
	}
}
