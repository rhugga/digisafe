package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/rhugga/digisafe/internal/app"
)

func TestRun(t *testing.T) {
	var out, stderr bytes.Buffer
	a := app.New()
	a.Out = &out
	if code := run(context.Background(), a, []string{"digisafe", "record", "types"}, &stderr); code != 0 || !strings.Contains(out.String(), "bank_card") {
		t.Errorf("types: exit %d, out %q, stderr %q", code, out.String(), stderr.String())
	}
	stderr.Reset()
	if code := run(context.Background(), a, []string{"digisafe", "--dir", t.TempDir(), "record", "list"}, &stderr); code != 1 ||
		!strings.HasPrefix(stderr.String(), "digisafe: no safe at") {
		t.Errorf("list without a safe: exit %d, stderr %q", code, stderr.String())
	}
}
