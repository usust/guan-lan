package runtime

import (
	"huashan/internal/environment"
	"testing"
)

func TestServiceResourceLimits(t *testing.T) {
	for _, tc := range []struct {
		name              string
		service           environment.Service
		memory, cpu, pids int64
	}{
		{"default", environment.Service{}, 0, 0, -1},
		{"legacy values ignored", environment.Service{MemoryMB: 512, CPU: 1, PidsLimit: 256}, 0, 0, -1},
		{"explicit limits", environment.Service{ResourceLimitsEnabled: true, MemoryMB: 1024, CPU: 2.5, PidsLimit: 512}, 1024 << 20, 2500000000, 512},
		{"enabled unlimited", environment.Service{ResourceLimitsEnabled: true}, 0, 0, -1},
		{"memory only", environment.Service{ResourceLimitsEnabled: true, MemoryMB: 2048}, 2048 << 20, 0, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			memory, cpu, pids := serviceResourceLimits(tc.service)
			if memory != tc.memory || cpu != tc.cpu || pids != tc.pids {
				t.Fatalf("got %d/%d/%d, want %d/%d/%d", memory, cpu, pids, tc.memory, tc.cpu, tc.pids)
			}
		})
	}
}

func TestResourcePolicyMatchesDockerNormalization(t *testing.T) {
	for _, tc := range []struct {
		name                                                         string
		memory, cpu, pids, actualMemory, actualCPU, actualPids, swap int64
		want                                                         bool
	}{
		{"unlimited explicit", 0, 0, -1, 0, 0, -1, -1, true},
		{"unlimited normalized", 0, 0, -1, 0, 0, 0, 0, true},
		{"cpu only normalized", 0, 1000000000, -1, 0, 1000000000, 0, 0, true},
		{"memory cap unlimited swap", 512 << 20, 0, -1, 512 << 20, 0, 0, -1, true},
		{"memory swap default rejected", 512 << 20, 0, -1, 512 << 20, 0, 0, 0, false},
		{"unexpected memory cap", 0, 0, -1, 512 << 20, 0, 0, 0, false},
		{"unexpected cpu cap", 0, 0, -1, 0, 1000000000, 0, 0, false},
		{"unexpected pid cap", 0, 0, -1, 0, 0, 256, 0, false},
		{"missing requested pid cap", 0, 0, 256, 0, 0, 0, 0, false},
		{"requested pid cap", 0, 0, 256, 0, 0, 256, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var x containerInfo
			x.HostConfig.Memory, x.HostConfig.NanoCPUs, x.HostConfig.PidsLimit, x.HostConfig.MemorySwap = tc.actualMemory, tc.actualCPU, tc.actualPids, tc.swap
			if got := resourcePolicyMatches(x, tc.memory, tc.cpu, tc.pids); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
