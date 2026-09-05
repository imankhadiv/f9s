package ui

import "testing"

func TestResolveCommand(t *testing.T) {
	cases := map[string]string{
		"jobs": "jobs", "j": "jobs", "tm": "tms", "taskmanagers": "tms",
		"sp": "savepoints", "led": "savepoints", "ctx": "contexts",
		"k8s": "k8s", "sql": "sql", "q": "quit", "quit": "quit",
		"s":   "savepoints", // ambiguous prefix resolves in table order
		"zzz": "", "": "",
	}
	for in, want := range cases {
		if got := resolveCommand(in); got != want {
			t.Errorf("resolveCommand(%q) = %q, want %q", in, got, want)
		}
	}
}
