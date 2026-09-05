package views

import (
	"strings"
	"testing"

	"github.com/imankhadiv/f9s/internal/model"
)

func TestPlanComparison(t *testing.T) {
	jar := map[string]string{"aaa": "Source", "bbb": "Window"}
	origin := []model.PlanNode{{ID: "aaa", Description: "Source"}, {ID: "ccc", Description: "KeyedProcess"}}
	var missing []string
	for _, n := range origin {
		if _, ok := jar[n.ID]; !ok {
			missing = append(missing, n.Description)
		}
	}
	if len(missing) != 1 || !strings.Contains(missing[0], "KeyedProcess") {
		t.Fatalf("mismatch detection broken: %v", missing)
	}
}
