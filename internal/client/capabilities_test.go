package client

import "testing"

func TestCapabilitiesForFlinkVersion(t *testing.T) {
	tests := []struct {
		version string
		want23  bool
	}{
		{version: "2.3.0", want23: true},
		{version: "2.3-SNAPSHOT", want23: true},
		{version: "v2.4.1", want23: true},
		{version: "2.2.1", want23: false},
		{version: "1.20.2", want23: false},
		{version: ""},
		{version: "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			got := CapabilitiesForVersion(tt.version)
			if got.Applications != tt.want23 || got.RescaleHistory != tt.want23 {
				t.Fatalf("CapabilitiesForVersion(%q) = %+v, want Flink 2.3 features %v", tt.version, got, tt.want23)
			}
		})
	}
}

func TestClientCapabilitiesUsesObservedClusterVersion(t *testing.T) {
	cl := testClient(t)
	if got := cl.Capabilities(); got.Applications || got.RescaleHistory {
		t.Fatalf("capabilities before overview = %+v, want conservative defaults", got)
	}
	if _, err := cl.Overview(); err != nil {
		t.Fatal(err)
	}
	if got := cl.Capabilities(); got.Applications || got.RescaleHistory {
		t.Fatalf("Flink 2.1 capabilities = %+v, want no 2.3-only features", got)
	}
}
