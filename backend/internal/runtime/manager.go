package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"huashan/internal/auth"
	"huashan/internal/environment"
)

type Manager struct {
	DB             *sql.DB
	Engine         *Engine
	Root, Platform string
	PublicHost     string
	slots          chan struct{}
}
type Endpoint struct {
	Service  string `json:"service"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	Address  string `json:"address"`
}
type Instance struct {
	ID        string     `json:"id"`
	State     string     `json:"state"`
	ExpiresAt int64      `json:"expires_at"`
	Endpoints []Endpoint `json:"endpoints"`
}

func NewManager(db *sql.DB, root, platform, endpoint string) (*Manager, error) {
	e, err := NewEngine(endpoint)
	if err != nil {
		return nil, err
	}
	return &Manager{DB: db, Engine: e, Root: root, Platform: platform, slots: make(chan struct{}, 1)}, nil
}
func (m *Manager) lock() error {
	select {
	case m.slots <- struct{}{}:
		return nil
	default:
		return fmt.Errorf("运行环境正在操作，请稍后重试")
	}
}
func (m *Manager) unlock() { <-m.slots }
func (m *Manager) labels(id string) map[string]string {
	return map[string]string{"huashan.platform": m.Platform, "huashan.instance": id}
}
func (m *Manager) verifyNetwork(ctx context.Context, id string) error {
	var x struct {
		Internal bool
		Driver   string
		Labels   map[string]string
		Options  map[string]string
	}
	if err := m.Engine.call(ctx, "GET", "/networks/"+url.PathEscape(id), nil, &x); err != nil {
		return err
	}
	if x.Internal || x.Driver != "bridge" || x.Labels["huashan.platform"] != m.Platform || x.Options["com.docker.network.bridge.gateway_mode_ipv4"] != "nat" {
		return fmt.Errorf("独立端口映射网络校验失败")
	}
	return nil
}
func (m *Manager) create(ctx context.Context, id string, manifest environment.Manifest, images map[string]string) (Instance, error) {
	result := Instance{ID: id, State: "RUNNING", Endpoints: []Endpoint{}}
	name := "huashan-" + id
	var network struct {
		ID string `json:"Id"`
	}
	progress(ctx, "创建独立网络并检查网络隔离")
	body := map[string]any{"Name": name, "Driver": "bridge", "Internal": false, "EnableIPv6": false, "Labels": m.labels(id), "Options": map[string]string{"com.docker.network.bridge.enable_icc": "true", "com.docker.network.bridge.gateway_mode_ipv4": "nat"}}
	if err := m.Engine.call(ctx, "POST", "/networks/create", body, &network); err != nil {
		return result, err
	}
	if err := m.verifyNetwork(ctx, network.ID); err != nil {
		return result, err
	}
	services := map[string]environment.Service{}
	for _, s := range manifest.Services {
		services[s.Key] = s
	}
	done := map[string]bool{}
	containers := map[string]string{}
	var start func(string) error
	start = func(key string) error {
		if done[key] {
			return nil
		}
		service := services[key]
		for _, dep := range service.DependsOn {
			if err := start(dep); err != nil {
				return err
			}
		}
		progress(ctx, "创建并启动服务 "+key)
		exposed := map[string]any{}
		bindings := map[string]any{}
		for _, p := range service.Ports {
			port := fmt.Sprintf("%d/%s", p.Port, p.Protocol)
			exposed[port] = map[string]any{}
			if p.Public {
				bindings[port] = []map[string]string{{"HostIp": "0.0.0.0", "HostPort": ""}}
			}
		}
		memory, cpu, pids := serviceResourceLimits(service)
		config := map[string]any{"Image": images[service.Image], "Hostname": service.Key, "Labels": m.labels(id), "ExposedPorts": exposed,
			"HostConfig":       map[string]any{"NetworkMode": network.ID, "Privileged": false, "CapDrop": []string{"ALL"}, "CapAdd": startupCapabilities, "SecurityOpt": []string{"no-new-privileges:true"}, "Memory": memory, "MemorySwap": int64(-1), "NanoCpus": cpu, "PidsLimit": pids, "PortBindings": bindings, "RestartPolicy": map[string]string{"Name": "no"}, "LogConfig": map[string]any{"Type": "json-file", "Config": map[string]string{"max-size": "5m", "max-file": "1"}}},
			"NetworkingConfig": map[string]any{"EndpointsConfig": map[string]any{network.ID: map[string]any{"Aliases": []string{service.Key}}}}}
		var container struct {
			ID string `json:"Id"`
		}
		if err := m.Engine.call(ctx, "POST", "/containers/create?name="+name+"-"+service.Key, config, &container); err != nil {
			return fmt.Errorf("创建服务 %s: %w", key, err)
		}
		if err := m.Engine.call(ctx, "POST", "/containers/"+container.ID+"/start", nil, nil); err != nil {
			return fmt.Errorf("启动服务 %s: %w", key, err)
		}
		// A running process must remain alive; image healthchecks, when provided,
		// must become healthy. Public TCP listeners are checked from the host.
		progress(ctx, "检查服务 "+key+" 的健康状态和安全策略")
		started := time.Now()
		deadline := started.Add(30 * time.Second)
		nextReport := started.Add(10 * time.Second)
		stable := 0
		for {
			x, err := m.Engine.inspect(ctx, container.ID)
			if err != nil {
				return err
			}
			if !x.State.Running {
				return fmt.Errorf("系统 %s 启动后退出（退出码 %d，内存不足终止：%t）：%s", serviceLabel(service), x.State.ExitCode, x.State.OOMKilled, x.State.Error)
			}
			if x.HostConfig.Privileged || x.HostConfig.NetworkMode != network.ID || len(x.NetworkSettings.Networks) != 1 || !contains(x.HostConfig.CapDrop, "ALL") || !safeCapabilities(x.HostConfig.CapAdd) || !contains(x.HostConfig.SecurityOpt, "no-new-privileges") || len(x.HostConfig.Binds) > 0 {
				return fmt.Errorf("服务 %s 的隔离策略未生效", key)
			}
			if !resourcePolicyMatches(x, memory, cpu, pids) {
				return fmt.Errorf("服务 %s 的资源策略未生效（内存 %d/%d，CPU %d/%d，进程数 %d/%d，swap %d；实际值/预期值）", key, x.HostConfig.Memory, memory, x.HostConfig.NanoCPUs, cpu, x.HostConfig.PidsLimit, pids, x.HostConfig.MemorySwap)
			}
			for _, mount := range x.Mounts {
				if mount.Type == "bind" {
					return fmt.Errorf("服务 %s 存在宿主机目录挂载", key)
				}
			}
			deadline = started.Add(healthWait(x))
			healthy := x.State.Health == nil || x.State.Health.Status == "healthy"
			if healthy {
				stable++
			} else {
				stable = 0
			}
			if stable >= 3 {
				break
			}
			if time.Now().After(deadline) {
				return healthFailure(service, x, healthWait(x))
			}
			if time.Now().After(nextReport) {
				progress(ctx, fmt.Sprintf("等待系统 %s 健康检查（已等待 %d 秒，最多 %d 秒）", serviceLabel(service), int(time.Since(started).Seconds()), int(healthWait(x).Seconds())))
				nextReport = time.Now().Add(10 * time.Second)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(500 * time.Millisecond):
			}
		}
		containers[key] = container.ID
		done[key] = true
		return nil
	}
	for _, s := range manifest.Services {
		if err := start(s.Key); err != nil {
			return result, err
		}
	}
	progress(ctx, "检查公开端口映射和 TCP 连通性")
	endpoints, err := m.publishedEndpoints(ctx, manifest, containers)
	if err != nil {
		return result, err
	}
	result.Endpoints = endpoints
	return result, nil
}

// Docker may normalize unlimited swap/PIDs to zero or null (decoded as zero).
// With a finite memory limit, swap must still explicitly remain unlimited.
func resourcePolicyMatches(x containerInfo, memory, cpu, pids int64) bool {
	swapOK := x.HostConfig.MemorySwap == -1 || (memory == 0 && x.HostConfig.MemorySwap == 0)
	pidsOK := x.HostConfig.PidsLimit == pids || (pids <= 0 && x.HostConfig.PidsLimit <= 0)
	return x.HostConfig.Memory == memory && x.HostConfig.NanoCPUs == cpu && swapOK && pidsOK
}

// Limits are opt-in so legacy package defaults cannot silently restrict services.
// Swap remains available even when an explicit memory limit is configured.
func serviceResourceLimits(service environment.Service) (memory, cpu, pids int64) {
	pids = -1
	if service.ResourceLimitsEnabled {
		memory = int64(service.MemoryMB) << 20
		cpu = int64(service.CPU * 1e9)
		if service.PidsLimit > 0 {
			pids = service.PidsLimit
		}
	}
	return
}

func contains(values []string, prefix string) bool {
	for _, v := range values {
		if v == prefix || strings.HasPrefix(v, prefix+":") {
			return true
		}
	}
	return false
}

// Cleanup discovers resources by platform AND instance labels, including a
// resource whose create response was lost. Failed cleanup keeps DB leases.
func (m *Manager) cleanup(ctx context.Context, id string) error {
	labels := m.labels(id)
	var containers []struct {
		ID string `json:"Id"`
	}
	if err := m.Engine.call(ctx, "GET", "/containers/json?all=1&filters="+filter(labels), nil, &containers); err != nil {
		return err
	}
	for _, c := range containers {
		if err := m.Engine.call(ctx, "DELETE", "/containers/"+c.ID+"?force=1&v=1", nil, nil); err != nil {
			return err
		}
	}
	var networks []struct {
		ID string `json:"Id"`
	}
	if err := m.Engine.call(ctx, "GET", "/networks?filters="+filter(labels), nil, &networks); err != nil {
		return err
	}
	for _, n := range networks {
		if err := m.Engine.call(ctx, "DELETE", "/networks/"+n.ID, nil, nil); err != nil {
			return err
		}
	}
	_, err := m.DB.ExecContext(ctx, "DELETE FROM runtime_instances WHERE id=?", id)
	return err
}
func (m *Manager) compensate(id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return m.cleanup(ctx, id)
}
func (m *Manager) packageInfo(ctx context.Context, question string) (environment.Manifest, string, string, string, error) {
	var manifest environment.Manifest
	var raw, path, sha string
	err := m.DB.QueryRowContext(ctx, "SELECT environment_json,package_path,package_sha256 FROM challenges WHERE id=?", question).Scan(&raw, &path, &sha)
	if err != nil {
		return manifest, raw, path, sha, err
	}
	if raw == "null" || path == "" || sha == "" || filepath.Base(path) != path || len(path) != 68 {
		return manifest, raw, path, sha, fmt.Errorf("题目需要先上传并校验完整环境包")
	}
	if err = json.Unmarshal([]byte(raw), &manifest); err == nil {
		err = manifest.Validate()
	}
	return manifest, raw, filepath.Join(m.Root, "uploads/packages", path), sha, err
}
func (m *Manager) Verify(ctx context.Context, question string, version int) error {
	if err := m.lock(); err != nil {
		return err
	}
	defer m.unlock()
	var actual int
	var status string
	if err := m.DB.QueryRowContext(ctx, "SELECT s.version,s.status FROM challenge_sets s JOIN challenges q ON q.set_id=s.id WHERE q.id=?", question).Scan(&actual, &status); err != nil {
		return err
	}
	if actual != version || (status != "DRAFT" && status != "CLOSED") {
		return fmt.Errorf("内容已变化或已发布，请刷新后重试")
	}
	manifest, raw, path, sha, err := m.packageInfo(ctx, question)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute+time.Duration(len(manifest.Services))*5*time.Minute)
	defer cancel()
	// A failed repeat validation must not leave an earlier success usable.
	if _, err = m.DB.ExecContext(ctx, "DELETE FROM runtime_verifications WHERE challenge_id=?", question); err != nil {
		return err
	}
	progress(ctx, "检查 Docker Engine")
	engineID, err := m.Engine.Identity(ctx)
	if err != nil {
		return err
	}
	progress(ctx, "校验环境包并导入镜像")
	images, err := m.Engine.Import(ctx, path, sha, filepath.Join(m.Root, "temp"), manifest)
	if err != nil {
		return err
	}
	id := auth.RandomToken()
	_, createErr := m.create(ctx, id, manifest, images)
	if createErr != nil {
		progress(ctx, "试运行失败："+createErr.Error())
	}
	progress(ctx, "清理验收容器和网络")
	cleanupErr := m.compensate(id)
	if cleanupErr == nil {
		progress(ctx, "测试容器和网络清理完成")
	}
	if createErr != nil {
		return errors.Join(createErr, cleanupErr)
	}
	if cleanupErr != nil {
		return fmt.Errorf("试运行成功但清理失败，暂不能发布: %w", cleanupErr)
	}
	progress(ctx, "保存验收结果")
	result, err := m.DB.ExecContext(ctx, `INSERT OR REPLACE INTO runtime_verifications SELECT q.id,q.package_sha256,q.environment_json,?,? FROM challenges q JOIN challenge_sets s ON s.id=q.set_id WHERE q.id=? AND q.environment_json=? AND q.package_sha256=? AND s.version=? AND s.status IN ('DRAFT','CLOSED')`, engineID, time.Now().UnixMilli(), question, raw, sha, version)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return fmt.Errorf("验收期间内容已变化，请重新验收")
	}
	return err
}
func (m *Manager) instanceStatus(ctx context.Context, user, question string) (Instance, error) {
	var x Instance
	var raw string
	err := m.DB.QueryRowContext(ctx, "SELECT id,state,details,expires_at FROM runtime_instances WHERE user_id=? AND challenge_id=?", user, question).Scan(&x.ID, &x.State, &raw, &x.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Instance{State: "STOPPED", Endpoints: []Endpoint{}}, nil
	}
	if err != nil {
		return x, err
	}
	err = json.Unmarshal([]byte(raw), &x.Endpoints)
	if err == nil {
		for i := range x.Endpoints {
			host, port, splitErr := net.SplitHostPort(x.Endpoints[i].Address)
			ip := net.ParseIP(host)
			if splitErr == nil && (host == "localhost" || (ip != nil && ip.IsLoopback())) {
				x.Endpoints[i].Address = net.JoinHostPort(m.endpointHost(), port)
			}
		}
	}
	if err == nil && x.State == "RUNNING" {
		alive, checkErr := m.alive(ctx, x.ID, question)
		if checkErr != nil {
			return x, checkErr
		}
		if !alive || x.ExpiresAt <= time.Now().UnixMilli() {
			x.State = "FAILED"
			x.Endpoints = []Endpoint{}
		}
	}
	return x, err
}

func (m *Manager) alive(ctx context.Context, id, question string) (bool, error) {
	parent, keys, err := m.runtimeTarget(ctx, question)
	if err != nil {
		return false, err
	}
	var raw string
	if err := m.DB.QueryRowContext(ctx, "SELECT environment_json FROM challenges WHERE id=?", parent).Scan(&raw); err != nil {
		return false, err
	}
	var manifest environment.Manifest
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return false, err
	}
	manifest, err = scopedManifest(manifest, keys)
	if err != nil {
		return false, err
	}
	expected := len(manifest.Services)
	var containers []struct {
		ID    string `json:"Id"`
		State string
	}
	if err := m.Engine.call(ctx, "GET", "/containers/json?all=1&filters="+filter(m.labels(id)), nil, &containers); err != nil {
		return false, err
	}
	if len(containers) != expected {
		return false, nil
	}
	for _, c := range containers {
		if c.State != "running" {
			return false, nil
		}
	}
	return true, nil
}
func (m *Manager) Start(ctx context.Context, user, question string) (Instance, error) {
	if err := m.lock(); err != nil {
		return Instance{}, err
	}
	defer m.unlock()
	progress(ctx, "检查启动条件")
	parent, keys, err := m.runtimeTarget(ctx, question)
	if err != nil {
		return Instance{}, err
	}
	var end int64
	var engineID string
	now := time.Now().UnixMilli()
	err = m.DB.QueryRowContext(ctx, `SELECT s.ends_at,v.engine_id FROM challenges q JOIN challenge_sets s ON s.id=q.set_id JOIN runtime_verifications v ON v.challenge_id=q.id AND v.package_sha256=q.package_sha256 AND v.environment_json=q.environment_json WHERE q.id=? AND s.status='PUBLISHED' AND s.starts_at<=? AND s.ends_at>?`, parent, now, now).Scan(&end, &engineID)
	if err != nil {
		return Instance{}, fmt.Errorf("当前题目未发布、未开放、已结束或环境未通过验收")
	}
	currentID, err := m.Engine.Identity(ctx)
	if err != nil {
		return Instance{}, err
	}
	if currentID != engineID {
		return Instance{}, fmt.Errorf("Docker Engine 已变化，需要管理员重新验收")
	}
	existing, err := m.Status(ctx, user, question)
	if err != nil {
		return Instance{}, err
	}
	if existing.ID != "" {
		if existing.State == "RUNNING" && existing.ExpiresAt > now {
			progress(ctx, "复用已开启的完整环境，同步入口和到期时间")
			return existing, nil
		}
		if err = m.cleanup(ctx, existing.ID); err != nil {
			return Instance{}, err
		}
	}
	// A failed direct instance may have hidden a healthy shared environment.
	if shared, err := m.reusableInstance(ctx, user, question); err != nil {
		return Instance{}, err
	} else if shared.ID != "" {
		progress(ctx, "复用已开启的完整环境，同步入口和到期时间")
		return m.bindInstance(ctx, user, question, shared)
	}
	var count int
	// Global capacity is determined by Docker and the host's available resources,
	// rather than an arbitrary number of environment instances.
	if err = m.DB.QueryRowContext(ctx, "SELECT count(*) FROM runtime_instances WHERE user_id=?", user).Scan(&count); err != nil {
		return Instance{}, err
	}
	if count >= 2 {
		return Instance{}, fmt.Errorf("每位选手最多同时运行 2 道题，请先停止其他环境")
	}
	manifest, _, path, sha, err := m.packageInfo(ctx, parent)
	if err != nil {
		return Instance{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute+time.Duration(len(manifest.Services))*5*time.Minute)
	defer cancel()
	id := auth.RandomToken()
	expires := time.Now().Add(time.Hour).UnixMilli()
	if expires > end {
		expires = end
	}
	if _, err = m.DB.ExecContext(ctx, "INSERT INTO runtime_instances VALUES(?,?,?,'STARTING','[]',?)", id, user, question, expires); err != nil {
		return Instance{}, err
	}
	fail := func(cause error) (Instance, error) { return Instance{}, errors.Join(cause, m.compensate(id)) }
	progress(ctx, "检查并复用本机镜像")
	images, err := m.Engine.ReadyImages(ctx, path, sha, filepath.Join(m.Root, "temp"), manifest)
	if err != nil {
		return fail(err)
	}
	manifest, err = scopedManifest(manifest, keys)
	if err != nil {
		return fail(err)
	}
	x, err := m.create(ctx, id, manifest, images)
	if err != nil {
		return fail(err)
	}
	x.ExpiresAt = expires
	// Publication may have closed while Docker was starting.
	var open int
	if err = m.DB.QueryRowContext(ctx, `SELECT count(*) FROM challenges q JOIN challenge_sets s ON s.id=q.set_id JOIN users u ON u.id=? WHERE q.id=? AND s.status='PUBLISHED' AND s.ends_at>? AND u.status='ACTIVE'`, user, parent, time.Now().UnixMilli()).Scan(&open); err != nil {
		return fail(err)
	}
	if open != 1 {
		return fail(fmt.Errorf("环境启动期间比赛已结束或账号已停用"))
	}
	details, _ := json.Marshal(x.Endpoints)
	if _, err = m.DB.ExecContext(ctx, "UPDATE runtime_instances SET state='RUNNING',details=? WHERE id=?", string(details), id); err != nil {
		return fail(err)
	}
	return x, nil
}
func (m *Manager) Stop(ctx context.Context, user, question string) error {
	if err := m.lock(); err != nil {
		return err
	}
	defer m.unlock()
	x, err := m.Status(ctx, user, question)
	if err != nil || x.ID == "" {
		return err
	}
	return m.cleanup(ctx, x.ID)
}

// Reconcile is called at startup and once a minute. It removes expired, closed,
// disabled-user and interrupted instances, plus labeled crash leftovers.
func (m *Manager) Reconcile(ctx context.Context) error {
	if err := m.lock(); err != nil {
		return err
	}
	defer m.unlock()
	rows, err := m.DB.QueryContext(ctx, `SELECT r.id FROM runtime_instances r LEFT JOIN subtasks t ON t.id=r.challenge_id LEFT JOIN challenges q ON q.id=COALESCE(t.challenge_id,r.challenge_id) LEFT JOIN challenge_sets s ON s.id=q.set_id LEFT JOIN users u ON u.id=r.user_id WHERE r.state!='RUNNING' OR r.expires_at<=? OR s.status IS NULL OR s.status!='PUBLISHED' OR s.ends_at<=? OR u.status IS NULL OR u.status!='ACTIVE'`, time.Now().UnixMilli(), time.Now().UnixMilli())
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = m.cleanup(ctx, id); err != nil {
			return err
		}
	}
	rows, err = m.DB.QueryContext(ctx, "SELECT id,challenge_id FROM runtime_instances WHERE state='RUNNING'")
	if err != nil {
		return err
	}
	type activeInstance struct{ id, question string }
	var active []activeInstance
	for rows.Next() {
		var x activeInstance
		if err = rows.Scan(&x.id, &x.question); err != nil {
			rows.Close()
			return err
		}
		active = append(active, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, x := range active {
		alive, checkErr := m.alive(ctx, x.id, x.question)
		if checkErr != nil {
			return checkErr
		}
		if !alive {
			if err = m.cleanup(ctx, x.id); err != nil {
				return err
			}
		}
	}
	var resources []struct {
		ID     string `json:"Id"`
		Labels map[string]string
	}
	labels := map[string]string{"huashan.platform": m.Platform}
	for _, path := range []string{"/containers/json?all=1&filters=", "/networks?filters="} {
		resources = nil
		if err = m.Engine.call(ctx, "GET", path+filter(labels), nil, &resources); err != nil {
			return err
		}
		for _, r := range resources {
			id := r.Labels["huashan.instance"]
			if id == "" {
				continue
			}
			var n int
			if err = m.DB.QueryRowContext(ctx, "SELECT count(*) FROM runtime_instances WHERE id=?", id).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				if err = m.cleanup(ctx, id); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// runtimeTarget resolves a subtask's services and their transitive dependencies.
func (m *Manager) runtimeTarget(ctx context.Context, target string) (string, []string, error) {
	var parent, raw string
	err := m.DB.QueryRowContext(ctx, `SELECT t.challenge_id,r.service_keys_json FROM subtasks t JOIN subtask_resources r ON r.subtask_id=t.id WHERE t.id=?`, target).Scan(&parent, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return target, nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	var keys []string
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		return "", nil, err
	}
	if len(keys) == 0 {
		return "", nil, fmt.Errorf("该小题未绑定环境")
	}
	return parent, keys, nil
}
func scopedManifest(manifest environment.Manifest, keys []string) (environment.Manifest, error) {
	if keys == nil {
		return manifest, nil
	}
	selected := map[string]bool{}
	var add func(string) error
	add = func(key string) error {
		if selected[key] {
			return nil
		}
		for _, service := range manifest.Services {
			if service.Key == key {
				selected[key] = true
				for _, dependency := range service.DependsOn {
					if err := add(dependency); err != nil {
						return err
					}
				}
				return nil
			}
		}
		return fmt.Errorf("绑定的服务 %s 不存在", key)
	}
	for _, key := range keys {
		if err := add(key); err != nil {
			return manifest, err
		}
	}
	services := []environment.Service{}
	for _, service := range manifest.Services {
		if selected[service.Key] {
			services = append(services, service)
		}
	}
	manifest.Services = services
	return manifest, nil
}
