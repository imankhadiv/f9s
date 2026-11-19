package client

import (
	"strconv"
	"strings"
)

// Capabilities names REST features that are safe to expose for the observed
// Flink cluster version. Unknown versions stay conservative.
type Capabilities struct {
	Applications   bool
	RescaleHistory bool
}

// CapabilitiesForVersion maps Flink's release string to REST capabilities.
// Flink 2.3 introduced both the application and rescale-history endpoints.
func CapabilitiesForVersion(version string) Capabilities {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return Capabilities{}
	}
	major, ok := leadingInt(parts[0])
	if !ok {
		return Capabilities{}
	}
	minor, ok := leadingInt(parts[1])
	if !ok {
		return Capabilities{}
	}
	supported := major > 2 || major == 2 && minor >= 3
	return Capabilities{Applications: supported, RescaleHistory: supported}
}

func leadingInt(value string) (int, bool) {
	end := 0
	for end < len(value) && value[end] >= '0' && value[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, false
	}
	v, err := strconv.Atoi(value[:end])
	return v, err == nil
}

// Capabilities reports features for the version cached by Overview.
func (c *Client) Capabilities() Capabilities {
	return CapabilitiesForVersion(c.FlinkVersion())
}
