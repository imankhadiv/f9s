package invlog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInvlogWritesTrail(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	lg := New("prod", "1f13abcd", "my-job", "manual")
	if lg == nil {
		t.Fatal("New returned nil")
	}
	lg.Line("→ get_job(jid=1f13abcd)")
	lg.Close("HYPOTHESIS: the sink is slow.", "")
	files, _ := filepath.Glob(filepath.Join(Dir(), "*.log"))
	if len(files) != 1 {
		t.Fatalf("want 1 log file, got %d", len(files))
	}
	body, _ := os.ReadFile(files[0])
	for _, want := range []string{"my-job", "get_job", "DIAGNOSIS", "the sink is slow"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("log missing %q:\n%s", want, body)
		}
	}
}
