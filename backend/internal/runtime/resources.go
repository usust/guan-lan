package runtime

import (
	"context"
	"time"
)

type Resources struct {
	Available   bool   `json:"available"`
	Containers  int    `json:"containers"`
	Running     int    `json:"running"`
	MemoryUsed  uint64 `json:"memory_used"`
	CPUs        int    `json:"cpus"`
	MemoryTotal uint64 `json:"memory_total"`
	Error       string `json:"error,omitempty"`
}

// ResourceUsage reads Docker-wide counts and actual running-container memory.
func ResourceUsage(ctx context.Context, endpoint string) Resources {
	result := Resources{}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	engine, err := NewEngine(endpoint)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer engine.client.CloseIdleConnections()
	var info struct {
		Containers        int
		ContainersRunning int
		MemTotal          uint64
		NCPU              int
	}
	if err = engine.call(ctx, "GET", "/info", nil, &info); err != nil {
		result.Error = err.Error()
		return result
	}
	result.CPUs = info.NCPU
	result.Containers, result.Running, result.MemoryTotal = info.Containers, info.ContainersRunning, info.MemTotal
	var containers []struct {
		ID string `json:"Id"`
	}
	if err = engine.call(ctx, "GET", "/containers/json", nil, &containers); err != nil {
		result.Error = err.Error()
		return result
	}
	for _, container := range containers {
		var stats struct {
			Memory struct{ Usage uint64 } `json:"memory_stats"`
		}
		if err = engine.call(ctx, "GET", "/containers/"+container.ID+"/stats?stream=false&one-shot=true", nil, &stats); err != nil {
			result.Error = err.Error()
			return result
		}
		result.MemoryUsed += stats.Memory.Usage
	}
	result.Available = true
	return result
}
