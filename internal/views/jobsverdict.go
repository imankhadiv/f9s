package views

import (
	"fmt"
	"sort"
	"strings"

	"github.com/imankhadiv/f9s/internal/model"
	"github.com/imankhadiv/f9s/internal/sentinel"
)

// verdictNames is how many troubled jobs the line names before summarising.
const verdictNames = 3

// jobsVerdict summarises the cluster in one line: the jobs list is the
// landing screen, and a table of rows is data, not an answer. level is
// "ok", "warn" or "crit" ("" when there is nothing to say).
func jobsVerdict(jobs []model.JobOverview, extras map[string]jobExtra, alerts []sentinel.Alert) (string, string) {
	if len(jobs) == 0 {
		return "", ""
	}
	firing := map[string]string{} // job name → alert kind
	for _, a := range alerts {
		if a.Open() {
			firing[a.Job] = a.Kind
		}
	}

	running := 0
	trouble := map[string]string{} // job name → why
	for _, j := range jobs {
		switch j.State {
		case "RUNNING":
			running++
		case "FINISHED", "CANCELED":
			continue // terminal states are history, not incidents
		default: // RESTARTING, FAILING, FAILED, SUSPENDED…
			trouble[j.Name] = j.State
		}
		if kind, ok := firing[j.Name]; ok {
			trouble[j.Name] = kind
		}
		// a RUNNING job whose tasks are not all running is starved or still
		// deploying — the fastest read on slot starvation
		if j.State == "RUNNING" && j.Tasks.Total > 0 && j.Tasks.Running < j.Tasks.Total {
			trouble[j.Name] = fmt.Sprintf("%d/%d tasks running", j.Tasks.Running, j.Tasks.Total)
		}
		if e, ok := extras[j.JID]; ok && e.ckptState == "FAILED" {
			if _, worse := trouble[j.Name]; !worse {
				trouble[j.Name] = "checkpoint failing"
			}
		}
	}

	if len(trouble) == 0 {
		return fmt.Sprintf("● HEALTHY — %d running · no alerts · checkpoints current", running), "ok"
	}

	names := make([]string, 0, len(trouble))
	for n := range trouble {
		names = append(names, n)
	}
	sort.Strings(names)
	parts := make([]string, 0, verdictNames+1)
	for i, n := range names {
		if i == verdictNames {
			parts = append(parts, fmt.Sprintf("+%d more", len(names)-verdictNames))
			break
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", truncate(n, 28), trouble[n]))
	}
	return fmt.Sprintf("● NEEDS ATTENTION — %d of %d jobs: %s", len(trouble), len(jobs), strings.Join(parts, " · ")), "crit"
}
