package runtime

import (
	"fmt"
	"huashan/internal/environment"
	"strings"
	"time"
)

func serviceLabel(s environment.Service) string {
	if strings.TrimSpace(s.Name) != "" {
		return s.Name
	}
	return s.Key
}

func healthWait(x containerInfo) time.Duration {
	const minimum = 30 * time.Second
	const maximum = 5 * time.Minute
	h := x.Config.Healthcheck
	if x.State.Health == nil || h == nil {
		return minimum
	}
	// Bound individual values before arithmetic to avoid overflow from image metadata.
	bounded := func(n int64, fallback time.Duration) time.Duration {
		if n <= 0 {
			return fallback
		}
		if n > int64(maximum) {
			return maximum
		}
		return time.Duration(n)
	}
	retries := h.Retries
	if retries <= 0 {
		retries = 3
	}
	if retries > 10 {
		retries = 10
	}
	budget := bounded(h.StartPeriod, 0) + time.Duration(retries)*(bounded(h.Interval, 30*time.Second)+bounded(h.Timeout, 30*time.Second)) + 5*time.Second
	if budget < minimum {
		return minimum
	}
	if budget > maximum {
		return maximum
	}
	return budget
}

func healthFailure(s environment.Service, x containerInfo, waited time.Duration) error {
	status := "未知"
	output := "无健康检查输出"
	if x.State.Health != nil {
		status = x.State.Health.Status
		logs := x.State.Health.Log
		if len(logs) > 0 {
			last := logs[len(logs)-1]
			output = fmt.Sprintf("退出码 %d：%s", last.ExitCode, strings.TrimSpace(last.Output))
			runes := []rune(output)
			if len(runes) > 2048 {
				output = string(runes[:2048]) + "…"
			}
		}
	}
	return fmt.Errorf("系统 %s 的镜像健康检查超时（已等待 %d 秒，状态 %s）。最近检查结果：%s。请根据输出检查应用启动、健康检查命令和依赖服务", serviceLabel(s), int(waited.Seconds()), status, output)
}

// These bounded container capabilities support entrypoints that initialize
// files and switch to an unprivileged application user. No host mounts or
// privileged/system-administration capabilities are granted.
var startupCapabilities = []string{"CHOWN", "DAC_OVERRIDE", "FOWNER", "SETUID", "SETGID", "NET_BIND_SERVICE"}

func safeCapabilities(values []string) bool {
	for _, value := range values {
		value = strings.TrimPrefix(strings.ToUpper(value), "CAP_")
		allowed := false
		for _, capability := range startupCapabilities {
			if value == capability {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}
	return true
}
