package views

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/imankhadiv/f9s/internal/metricts"
	"github.com/imankhadiv/f9s/internal/model"
)

func liveView() *VertexLive {
	ring := metricts.New(0)
	now := time.Now()
	for i := 0; i < 5; i++ {
		ring.Add(metricts.Key{Scope: metricts.ScopeVertex, ID: metricts.VertexID("j1", "v1"), Metric: metricts.Busy},
			metricts.Point{Time: now.Add(time.Duration(i-5) * 15 * time.Second), Value: float64(400 + i*100)})
	}
	return NewVertexLive(Deps{Metrics: ring}, "j1", "orders", "v1", "Window Aggregate", 4)
}

func liveMetrics() vertexLiveMsg {
	return vertexLiveMsg{stats: vertexStats{
		inRecs: 12400, inBytes: 3 << 20, outRecs: 12300, outBytes: 3 << 20,
		busy: 920, level: "high", bpMax: 74,
		subRecords: []float64{5900, 8100, 12400, 7000},
		inPool:     []float64{0.98, 0.9, 0.2, 0.3}, outPool: []float64{0.12, 0.1, 0.1, 0.1},
		wmLag: 1200, wmKnown: true,
	}}
}

func TestVertexLiveMetricsTab(t *testing.T) {
	v := liveView()
	v.Update(liveMetrics())
	body := v.View(120, 30)
	if !strings.Contains(strings.ToUpper(body), "WINDOW AGGREGATE") {
		t.Errorf("metrics tab does not name the vertex:\n%s", body)
	}
	for _, want := range []string{
		"in", "12.4k", "out", "12.3k", "busy", "92%", "backpressure", "74%",
		"watermark lag", "1s", "subtasks", "skew",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics tab missing %q:\n%s", want, body)
		}
	}
	if !strings.ContainsAny(body, "▁▂▃▄▅▆▇█") {
		t.Errorf("metrics tab lacks the ring sparkline:\n%s", body)
	}
}

func TestVertexLiveTabSwitch(t *testing.T) {
	v := liveView()
	if v.tab != tabMetrics {
		t.Fatal("opens on metrics")
	}
	v.Update(tea.KeyMsg{Type: tea.KeyTab})
	if v.tab != tabRows {
		t.Fatal("tab switches to rows")
	}
	v.Update(tea.KeyMsg{Type: tea.KeyTab})
	if v.tab != tabMetrics {
		t.Fatal("tab cycles back to metrics")
	}
}

func TestVertexLiveRowsTailsOnlyThisVertexTaskManagers(t *testing.T) {
	v := liveView()
	v.Update(vertexLiveMsg{tms: []string{"tm-a", "tm-b"}})
	v.Update(tea.KeyMsg{Type: tea.KeyTab})
	v.Update(vertexRowsMsg{rows: []rowLine{
		{tm: "tm-a", text: "+I[key-7, 41]"},
		{tm: "tm-b", text: "+I[key-3, 12]"},
	}, perSec: 3})

	body := v.View(120, 30)
	for _, want := range []string{"+I[key-7, 41]", "tm-a", "3.0 rows/s", "2 TaskManagers"} {
		if !strings.Contains(body, want) {
			t.Errorf("rows tab missing %q:\n%s", want, body)
		}
	}
}

// A vertex that prints nothing must say so, and say where to look instead —
// never leave an empty box that looks like a bug.
func TestVertexLiveRowsExplainsWhenThereIsNothingToTail(t *testing.T) {
	v := liveView()
	v.Update(vertexLiveMsg{tms: []string{"tm-a"}})
	v.Update(tea.KeyMsg{Type: tea.KeyTab})
	v.Update(vertexRowsMsg{}) // polled, nothing printed

	body := v.View(120, 30)
	for _, want := range []string{"prints nothing to stdout", "L", "t"} {
		if !strings.Contains(body, want) {
			t.Errorf("empty rows tab missing %q:\n%s", want, body)
		}
	}
}

func TestVertexLiveRowsPerSecond(t *testing.T) {
	cases := []struct {
		name       string
		prev, next int
		elapsed    time.Duration
		want       float64
	}{
		{"steady", 100, 130, 3 * time.Second, 10},
		{"first poll has no rate", 0, 50, 3 * time.Second, -1},
		{"log rotated away", 500, 20, 3 * time.Second, -1},
		{"no time passed", 10, 20, 0, -1},
	}
	for _, c := range cases {
		if got := rowsPerSec(c.prev, c.next, c.elapsed); got != c.want {
			t.Errorf("%s: rowsPerSec(%d,%d,%v) = %v, want %v", c.name, c.prev, c.next, c.elapsed, got, c.want)
		}
	}
}

func TestVertexLiveTailKeepsNewestLines(t *testing.T) {
	var b strings.Builder
	for i := 0; i < rowsKept+50; i++ {
		b.WriteString("line-" + string(rune('a'+i%26)) + "\n")
	}
	got := tailRows("tm-1", b.String())
	if len(got) != rowsKept {
		t.Fatalf("kept %d lines, want %d", len(got), rowsKept)
	}
	if got[0].tm != "tm-1" {
		t.Fatalf("row not tagged with its TaskManager: %+v", got[0])
	}
	if strings.TrimSpace(got[len(got)-1].text) == "" {
		t.Fatal("trailing blank line kept")
	}
}

func TestVertexLiveTaskManagersFromSubtasks(t *testing.T) {
	subs := []model.SubtaskInfo{
		{Subtask: 0, TaskManagerID: "tm-a"},
		{Subtask: 1, TaskManagerID: "tm-b"},
		{Subtask: 2, TaskManagerID: "tm-a"}, // same TM twice
		{Subtask: 3, TaskManagerID: ""},     // not deployed
	}
	got := taskManagersOf(subs)
	if len(got) != 2 || got[0] != "tm-a" || got[1] != "tm-b" {
		t.Fatalf("taskManagersOf = %v, want [tm-a tm-b]", got)
	}
}

func TestVertexLiveRendersSafelyAtAnyWidth(t *testing.T) {
	v := liveView()
	v.Update(liveMetrics())
	v.Update(vertexLiveMsg{tms: []string{"tm-a"}})
	for _, tab := range []int{0, 1} {
		if tab == 1 {
			v.Update(tea.KeyMsg{Type: tea.KeyTab})
			v.Update(vertexRowsMsg{rows: []rowLine{{tm: "tm-a", text: strings.Repeat("wide ", 80)}}, perSec: 1})
		}
		for _, w := range []int{60, 80, 200} {
			for _, line := range strings.Split(v.View(w, 24), "\n") {
				if got := lipgloss.Width(line); got > w {
					t.Errorf("tab %d width %d: line rendered %d columns", tab, w, got)
				}
			}
		}
	}
}

func TestDagEnterOpensTheLiveInspector(t *testing.T) {
	d := NewDagView(Deps{Metrics: metricts.New(0)}, "j1", "orders")
	d.plan = model.Plan{Nodes: []model.PlanNode{{ID: "v1"}}}
	d.job = model.Job{Vertices: []model.Vertex{{ID: "v1", Name: "Window Aggregate", Parallelism: 4}}}
	d.selVID = "v1"

	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter must open the live inspector")
	}
	pm, ok := cmd().(PushMsg)
	if !ok {
		t.Fatalf("enter produced %#v", cmd())
	}
	live, ok := pm.M.(*VertexLive)
	if !ok || live.vid != "v1" || live.par != 4 || live.name != "Window Aggregate" {
		t.Fatalf("pushed %#v", pm.M)
	}
}

// Flink answers the stdout request with an explanation (HTTP 200) when the
// TaskExecutor keeps no stdout file — under Docker/Kubernetes that is the
// normal case. It must never be rendered as if it were job output.
func TestVertexLiveRowsRecognisesMissingStdoutFile(t *testing.T) {
	body := "The file STDOUT does not exist on the TaskExecutor.\n" +
		`If you are using kubernetes mode, please use "kubectl logs <pod-name>" to get stdout content.`
	if !stdoutUnavailable(body) {
		t.Fatal("Flink's no-stdout-file answer must be recognised")
	}
	if stdoutUnavailable("+I[key-7, 41]\n+I[key-3, 12]") {
		t.Fatal("real output must not be mistaken for the explanation")
	}

	v := liveView()
	v.Update(vertexLiveMsg{tms: []string{"tm-a"}})
	v.Update(tea.KeyMsg{Type: tea.KeyTab})
	v.Update(vertexRowsMsg{unavailable: true})

	out := v.View(120, 30)
	if strings.Contains(out, "does not exist on the TaskExecutor") {
		t.Errorf("Flink's raw message leaked into the rows tab:\n%s", out)
	}
	for _, want := range []string{"keeps no stdout file", "docker logs", "kubectl logs"} {
		if !strings.Contains(out, want) {
			t.Errorf("explanation missing %q:\n%s", want, out)
		}
	}
}
