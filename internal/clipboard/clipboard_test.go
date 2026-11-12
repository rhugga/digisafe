package clipboard

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain doubles as the background clearer: ScheduleClear re-runs the current executable
// (here, the test binary) as `_clear-clipboard --after <d>`. In that mode it records the args
// and the hash it was sent to $DIGISAFE_CLEAR_OUT and exits.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == ClearCommand {
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		out := strings.Join(os.Args[1:], " ") + "\n" + line
		_ = os.WriteFile(os.Getenv("DIGISAFE_CLEAR_OUT"), []byte(out), 0o600)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestSum(t *testing.T) {
	if Sum("a") == Sum("b") || len(Sum("a")) != 64 {
		t.Error("Sum should be a 64-character sha256 hex digest that differs per value")
	}
}

func TestScheduleClearRunsTheClearer(t *testing.T) {
	out := filepath.Join(t.TempDir(), "clearer.out")
	t.Setenv("DIGISAFE_CLEAR_OUT", out)
	if err := ScheduleClear("hunter2", 30*time.Second); err != nil {
		t.Fatal(err)
	}
	var got []byte
	for i := 0; i < 100; i++ { // the clearer is detached: wait up to 5s for it
		if b, err := os.ReadFile(out); err == nil && strings.Contains(string(b), "\n") && len(b) > 70 {
			got = b
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	want := ClearCommand + " --after 30s\n" + Sum("hunter2") + "\n"
	if string(got) != want {
		t.Errorf("clearer got %q, want %q", got, want)
	}
	if strings.Contains(string(got), "hunter2") {
		t.Error("the value itself reached the clearer process")
	}
}
