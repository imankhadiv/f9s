package views

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/config"
	"github.com/imankhadiv/f9s/internal/metricts"
	"github.com/imankhadiv/f9s/internal/model"
)

func detailWithVertices() *JobDetail {
	d := NewJobDetail(Deps{Metrics: metricts.New(0)}, "j1", "orders")
	d.Update(jobDetailMsg{gen: d.gen, slots: 3, job: model.Job{
		State: "RUNNING",
		Vertices: []model.Vertex{
			{ID: "v-source", Name: "Source: kafka", Parallelism: 2},
			{ID: "v-window", Name: "Window(5m)", Parallelism: 4},
			{ID: "v-sink", Name: "Sink: s3", Parallelism: 2},
		},
	}})
	return d
}

// The keys that drill into "the selected vertex" must follow what the
// operator can SEE: the highlighted box in graph mode, the table cursor
// otherwise. They used to always read the table cursor, so in graph mode
// they silently targeted a different vertex.
func TestDrillDownFollowsTheVisibleSelection(t *testing.T) {
	d := detailWithVertices()
	d.Update(keyMsg("g")) // graph on
	if d.graph == nil {
		t.Fatal("g must build the graph")
	}
	d.graph.selVID = "v-sink"

	for _, k := range []string{"d", "e", "t"} {
		_, cmd := d.Update(keyMsg(k))
		if cmd == nil {
			t.Fatalf("%s produced no command", k)
		}
		pm, ok := cmd().(PushMsg)
		if !ok {
			t.Fatalf("%s pushed nothing: %#v", k, cmd())
		}
		live, ok := pm.M.(*VertexLive)
		if !ok {
			t.Fatalf("%s pushed %T, want the vertex inspector", k, pm.M)
		}
		if live.vid != "v-sink" {
			t.Errorf("%s drilled into %q, want the highlighted v-sink", k, live.vid)
		}
	}
}

func TestSelectionSurvivesTheGraphToggle(t *testing.T) {
	d := detailWithVertices()
	d.tbl.SetCursor(2) // Sink in the table

	d.Update(keyMsg("g"))
	if d.graph.selVID != "v-sink" {
		t.Fatalf("toggling to the graph lost the selection: %q", d.graph.selVID)
	}

	d.graph.selVID = "v-window"
	d.Update(keyMsg("g")) // back to the table
	if d.showGraph {
		t.Fatal("g must toggle the graph off")
	}
	if got := d.tbl.Cursor(); got != 1 {
		t.Fatalf("table cursor = %d, want 1 (the vertex highlighted in the graph)", got)
	}
}

// The embedded graph must not re-fetch what the job page already sampled.
func TestEmbeddedGraphReusesTheJobPageSamples(t *testing.T) {
	d := detailWithVertices()
	d.Update(keyMsg("g"))
	d.Update(backpressureMsg{vid: "v-window", level: "high", pct: 91, busy: 970,
		recs: []float64{100, 900}, mboxP99: -1, softBP: -1, debloat: -1})

	st, ok := d.graph.stats["v-window"]
	if !ok {
		t.Fatal("the graph did not receive the job page's sample")
	}
	if st.level != "high" || st.bpMax != 91 || st.busyMax != 97 {
		t.Fatalf("converted sample = %+v", st)
	}
	if st.skew <= 0 {
		t.Fatalf("skew not carried over: %+v", st)
	}
	if d.graph.embedded != true {
		t.Fatal("an embedded graph must know it is embedded (so it stops sampling)")
	}
	if cmd := d.graph.sampleCmds(); cmd != nil {
		t.Fatal("an embedded graph must not issue its own per-vertex sampling")
	}
}

// One key toggles the graph; the old second binding is gone.
func TestSingleGraphKey(t *testing.T) {
	d := detailWithVertices()
	_, cmd := d.Update(keyMsg("G"))
	if cmd != nil {
		t.Fatalf("G must no longer be bound, got %#v", cmd())
	}
	if d.showGraph {
		t.Fatal("G must not toggle the graph")
	}
	keys := map[string]bool{}
	for _, b := range d.Bindings() {
		keys[b.Help().Key] = true
	}
	if keys["G"] {
		t.Error("G is still advertised in the bindings")
	}
	if !keys["g"] {
		t.Error("g must be advertised as the graph toggle")
	}
}

func TestGraphKeysReachTheGraphWhenShown(t *testing.T) {
	d := detailWithVertices()
	d.Update(keyMsg("g"))
	d.graph.order = []string{"v-source", "v-window", "v-sink"}
	d.graph.selVID = "v-source"

	d.Update(tea.KeyMsg{Type: tea.KeyRight})
	if d.graph.selVID != "v-window" {
		t.Fatalf("→ did not move the box selection: %q", d.graph.selVID)
	}
}

// Deleting the pushed DAG view must not cost the graph's own keys: enter
// (live inspector) and O (parallelism pin) still have to reach it.
func TestGraphOwnKeysStillWorkWhenEmbedded(t *testing.T) {
	d := detailWithVertices()
	d.Update(keyMsg("g"))
	d.graph.selVID = "v-window"
	d.graph.job = d.job
	d.graph.plan = model.Plan{Nodes: []model.PlanNode{{ID: "v-window"}}}

	_, cmd := d.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("enter in graph mode must open the live inspector")
	}
	pm, ok := cmd().(PushMsg)
	if !ok {
		t.Fatalf("enter produced %#v", cmd())
	}
	live, ok := pm.M.(*VertexLive)
	if !ok || live.vid != "v-window" {
		t.Fatalf("enter pushed %#v", pm.M)
	}
}

// R (rescale) is a job-page action and must target the visible selection too.
func TestRescaleTargetsTheVisibleSelection(t *testing.T) {
	cfg := readWriteCfg()
	d := NewJobDetail(Deps{Ctx: "dev", Cfg: cfg, Metrics: metricts.New(0)}, "j1", "orders")
	d.Update(jobDetailMsg{gen: d.gen, job: model.Job{State: "RUNNING", Vertices: []model.Vertex{
		{ID: "v-a", Name: "A", Parallelism: 2}, {ID: "v-b", Name: "B", Parallelism: 4},
	}}})
	d.Update(keyMsg("g"))
	d.graph.selVID = "v-b"

	_, cmd := d.Update(keyMsg("R"))
	if cmd == nil {
		t.Fatal("R produced no command")
	}
	rm, ok := cmd().(RescaleMsg)
	if !ok {
		t.Fatalf("R produced %#v", cmd())
	}
	if rm.VID != "v-b" || rm.Current != 4 {
		t.Fatalf("rescale targeted %+v, want the highlighted v-b", rm)
	}
}

func readWriteCfg() *config.Config {
	return &config.Config{Contexts: map[string]config.Context{"dev": {}}}
}

func TestEnterFromTableOpensTheInspector(t *testing.T) {
	d := detailWithVertices()
	d.tbl.SetCursor(1)

	_, cmd := d.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("enter must open the vertex inspector from the table too")
	}
	pm, ok := cmd().(PushMsg)
	if !ok {
		t.Fatalf("enter produced %#v", cmd())
	}
	live, ok := pm.M.(*VertexLive)
	if !ok || live.vid != "v-window" || live.par != 4 {
		t.Fatalf("enter pushed %#v", pm.M)
	}
}
