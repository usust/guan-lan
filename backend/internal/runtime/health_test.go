package runtime

import (
	"encoding/json"
	"huashan/internal/environment"
	"strings"
	"testing"
	"time"
)

func TestHealthWaitHonorsImageSchedule(t *testing.T) {
	var x containerInfo
	if err := json.Unmarshal([]byte(`{"State":{"Running":true,"Health":{"Status":"starting"}},"Config":{"Healthcheck":{"StartPeriod":60000000000,"Interval":30000000000,"Timeout":10000000000,"Retries":3}}}`), &x); err != nil {
		t.Fatal(err)
	}
	if got := healthWait(x); got != 185*time.Second {
		t.Fatalf("got %v, want startup plus retries and stability allowance", got)
	}
	x.Config.Healthcheck.Interval = 1 << 62
	x.Config.Healthcheck.Retries = 1 << 30
	if got := healthWait(x); got != 5*time.Minute {
		t.Fatal(got)
	}
	x.State.Health = nil
	if got := healthWait(x); got != 30*time.Second {
		t.Fatal(got)
	}
}

func TestHealthFailureIncludesSystemNameAndProbeOutput(t *testing.T) {
	var x containerInfo
	if err := json.Unmarshal([]byte(`{"State":{"Health":{"Status":"unhealthy","Log":[{"ExitCode":1,"Output":"curl: connection refused"}]}}}`), &x); err != nil {
		t.Fatal(err)
	}
	message := healthFailure(environment.Service{Key: "container", Name: "财务系统"}, x, 185*time.Second).Error()
	for _, part := range []string{"财务系统", "185 秒", "unhealthy", "退出码 1", "connection refused"} {
		if !strings.Contains(message, part) {
			t.Fatal(message)
		}
	}
	if strings.Contains(message, "TCP 端口试运行") {
		t.Fatal("health failure confused with port check")
	}
}

func TestStartupCapabilitiesRemainBounded(t *testing.T) {
	if !safeCapabilities(startupCapabilities) || !safeCapabilities([]string{"CAP_SETGID", "CAP_CHOWN"}) {
		t.Fatal("required startup capabilities rejected")
	}
	for _, capability := range []string{"SYS_ADMIN", "SYS_PTRACE", "NET_ADMIN", "ALL", "CAP_SYS_ADMIN"} {
		if safeCapabilities([]string{capability}) {
			t.Fatalf("unsafe capability %s accepted", capability)
		}
	}
}
