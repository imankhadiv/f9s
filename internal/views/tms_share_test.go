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
