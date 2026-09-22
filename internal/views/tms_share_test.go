package views

import (
	"strings"
	"testing"

	"github.com/imankhadiv/f9s/internal/model"
)

func TestTaskManagersSlotShareInSessionMode(t *testing.T) {
	v := NewTaskManagers(Deps{})
	v.Update(tmsMsg{
		tms: []model.TaskManager{
			{ID: "tm-1", SlotsNumber: 4, FreeSlots: 0},
			{ID: "tm-2", SlotsNumber: 4, FreeSlots: 2},
		},
		names: map[string]string{"j-orders": "orders", "j-wc": "wordcount"},
	})
	v.Update(tmHeapMsg{id: "tm-1", heapPct: 40, gcTotal: -1, jobs: map[string]int{"j-orders": 3, "j-wc": 1}})
	v.Update(tmHeapMsg{id: "tm-2", heapPct: 40, gcTotal: -1, jobs: map[string]int{"j-orders": 2}})

	body := v.View(160, 20)
	if !strings.Contains(body, "slot share  orders 5 (62%) · wordcount 1 (12%) · free 2 of 8") {
		t.Fatalf("slot share line missing:\n%s", body)
	}
	if ctx, _ := v.AIContext(); !strings.Contains(ctx, "jobs 2") || !strings.Contains(ctx, "slot share") {
		t.Fatalf("AI context:\n%s", ctx)
	}
}

func TestTaskManagersNoShareLineForASingleJob(t *testing.T) {
	v := NewTaskManagers(Deps{})
	v.Update(tmsMsg{tms: []model.TaskManager{{ID: "tm-1", SlotsNumber: 2}}})
	v.Update(tmHeapMsg{id: "tm-1", heapPct: 10, gcTotal: -1, jobs: map[string]int{"j": 2}})
	if strings.Contains(v.View(160, 20), "slot share") {
		t.Fatal("an application cluster has nothing to share")
	}
}

func TestSlotShareForgetsDepartedTaskManagers(t *testing.T) {
	v := NewTaskManagers(Deps{})
	v.Update(tmsMsg{tms: []model.TaskManager{{ID: "tm-1", SlotsNumber: 2}, {ID: "tm-2", SlotsNumber: 2}},
		names: map[string]string{"a": "alpha", "b": "beta"}})
	v.Update(tmHeapMsg{id: "tm-1", heapPct: 1, gcTotal: -1, jobs: map[string]int{"a": 2}})
	v.Update(tmHeapMsg{id: "tm-2", heapPct: 1, gcTotal: -1, jobs: map[string]int{"b": 2}})
	v.Update(tmsMsg{tms: []model.TaskManager{{ID: "tm-1", SlotsNumber: 2}}, names: map[string]string{"a": "alpha"}})
	if shares := v.slotShares(); len(shares) != 1 || shares[0].name != "alpha" {
		t.Fatalf("departed TM still counted: %+v", shares)
	}
}

func TestShortJobNameDisambiguatesSQLJobs(t *testing.T) {
	if got := shortJobName("insert-into_default_catalog.default_database.chaos_agg_sink"); got != "insert→chaos_agg_sink" {
		t.Errorf("got %q", got)
	}
	if got := shortJobName("CarTopSpeedWindowingExample"); got != "CarTopSpeedWindowingExample" {
		t.Errorf("got %q", got)
	}
}
