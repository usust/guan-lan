package competition

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"huashan/internal/database"
	"huashan/internal/environment"
)

func setup(t *testing.T) (*Service, context.Context) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"), "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, id := range []string{"admin", "alice", "bob"} {
		role := "PLAYER"
		if id == "admin" {
			role = "ADMIN"
		}
		if _, err = db.Exec("INSERT INTO users VALUES(?,?,?,'hash',?,'ACTIVE',0)", id, id, id, role); err != nil {
			t.Fatal(err)
		}
	}
	return &Service{DB: db}, context.Background()
}
func draft() Set {
	return Set{Title: "Web 综合", StartsAt: time.Now().Add(-time.Hour).UnixMilli(), EndsAt: time.Now().Add(time.Hour).UnixMilli(), Questions: []Question{{Title: "Web", Category: "Web", Difficulty: "MEDIUM", Subtasks: []Subtask{{Title: "SQL", Content: "找到 Flag", Points: 100, Flag: "flag{test}"}, {Title: "权限提升", Points: 200, Flag: "flag{root}"}}}}}
}
func saveDraft(t *testing.T, s *Service, ctx context.Context, x Set) Set {
	t.Helper()
	id, err := s.Save(ctx, "admin", x)
	if err != nil {
		t.Fatal(err)
	}
	x, err = s.Get(ctx, id, "admin", true)
	if err != nil {
		t.Fatal(err)
	}
	return x
}
func publish(t *testing.T, s *Service, ctx context.Context, x Set) Set {
	t.Helper()
	if err := s.Transition(ctx, "admin", x.ID, "PUBLISHED", x.Version); err != nil {
		t.Fatal(err)
	}
	x, err := s.Get(ctx, x.ID, "admin", true)
	if err != nil {
		t.Fatal(err)
	}
	return x
}
func TestPublishSubmitAndImmutableContent(t *testing.T) {
	s, ctx := setup(t)
	x := saveDraft(t, s, ctx, draft())
	if _, err := s.Get(ctx, x.ID, "alice", false); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("draft leaked: %v", err)
	}
	items, err := s.List(ctx, false)
	if err != nil || len(items) != 0 {
		t.Fatal("draft listed", err)
	}
	// Blank flag during editing keeps its existing digest.
	x.Questions[0].Title = "修改题名"
	x = saveDraft(t, s, ctx, x)
	old := x
	x = publish(t, s, ctx, x)
	if _, err = s.Save(ctx, "admin", old); !errors.Is(err, ErrConflict) {
		t.Fatal("edited publication", err)
	}
	if err = s.Delete(ctx, "admin", x.ID, x.Version); !errors.Is(err, ErrConflict) {
		t.Fatal("deleted publication", err)
	}
	if err = s.Transition(ctx, "admin", x.ID, "PUBLISHED", x.Version); !errors.Is(err, ErrConflict) {
		t.Fatal("republished", err)
	}
	public, err := s.Get(ctx, x.ID, "alice", false)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(public)
	if strings.Contains(string(b), "flag{test}") || strings.Contains(string(b), "flag_hash") || public.Questions[0].Subtasks[0].FlagConfigured {
		t.Fatal("flag leaked")
	}
	sub := x.Questions[0].Subtasks[0].ID
	r, err := s.Submit(ctx, "alice", sub, "Flag{test}")
	if err != nil || r.Correct {
		t.Fatal("case folding", r, err)
	}
	r, err = s.Submit(ctx, "alice", sub, "flag{test}")
	if err != nil || !r.FirstSolve || r.AwardedPoints != 100 {
		t.Fatal(r, err)
	}
	r, err = s.Submit(ctx, "alice", sub, "flag{test}")
	if err != nil || r.FirstSolve || r.AwardedPoints != 0 {
		t.Fatal("duplicate score", r, err)
	}
	if _, err = s.Submit(ctx, "bob", sub, "flag{test}"); err != nil {
		t.Fatal(err)
	}
	ranks, err := s.Leaderboard(ctx, x.ID, false)
	if err != nil || len(ranks) != 2 || ranks[0].Points != 100 || ranks[0].Solved != 1 {
		t.Fatal(ranks, err)
	}
	history, err := s.History(ctx, "alice", x.ID)
	if err != nil || len(history) != 3 {
		t.Fatal(history, err)
	}
	public, err = s.Get(ctx, x.ID, "alice", false)
	if err != nil || !public.Questions[0].Subtasks[0].Solved || public.Questions[0].Subtasks[0].Attempts != 3 {
		t.Fatal(public, err)
	}
	if err = s.Transition(ctx, "admin", x.ID, "CLOSED", x.Version); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Submit(ctx, "alice", sub, "flag{test}"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("closed accepted", err)
	}
	ranks, err = s.Leaderboard(ctx, x.ID, true)
	if err != nil || len(ranks) != 2 {
		t.Fatal("scores lost", err)
	}
}
func TestConcurrentSubmissionsOnlyAwardOnce(t *testing.T) {
	s, ctx := setup(t)
	x := publish(t, s, ctx, saveDraft(t, s, ctx, draft()))
	id := x.Questions[0].Subtasks[0].ID
	var wg sync.WaitGroup
	results := make(chan SubmitResult, 10)
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r, err := s.Submit(ctx, "alice", id, "flag{test}"); results <- r; errs <- err }()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	awarded := 0
	for r := range results {
		awarded += r.AwardedPoints
	}
	if awarded != 100 {
		t.Fatal("awarded", awarded)
	}
	var n int
	s.DB.QueryRow("SELECT count(*) FROM solves").Scan(&n)
	if n != 1 {
		t.Fatal("duplicate solves", n)
	}
}
func TestVersionWindowsRateAndRollback(t *testing.T) {
	s, ctx := setup(t)
	x := saveDraft(t, s, ctx, draft())
	stale := x
	x.Title = "新内容"
	x = saveDraft(t, s, ctx, x)
	if _, err := s.Save(ctx, "admin", stale); !errors.Is(err, ErrConflict) {
		t.Fatal("stale edit", err)
	}
	x.Questions[0].Subtasks = append(x.Questions[0].Subtasks, Subtask{Title: "新小题", Points: 0})
	if _, err := s.Save(ctx, "admin", x); err == nil {
		t.Fatal("invalid points accepted")
	}
	x, err := s.Get(ctx, x.ID, "admin", true)
	if err != nil || len(x.Questions[0].Subtasks) != 2 {
		t.Fatal("partial save persisted", err)
	}
	x.StartsAt = time.Now().Add(time.Hour).UnixMilli()
	x.EndsAt = time.Now().Add(2 * time.Hour).UnixMilli()
	x = publish(t, s, ctx, saveDraft(t, s, ctx, x))
	if _, err = s.Submit(ctx, "alice", x.Questions[0].Subtasks[0].ID, "flag{test}"); !errors.Is(err, ErrClosed) {
		t.Fatal("early submit", err)
	}
	y := publish(t, s, ctx, saveDraft(t, s, ctx, draft()))
	sub := y.Questions[0].Subtasks[0].ID
	for i := 0; i < 20; i++ {
		if _, err = s.Submit(ctx, "alice", sub, "wrong"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.Submit(ctx, "alice", sub, "wrong"); !errors.Is(err, ErrRate) {
		t.Fatal("no rate limit", err)
	}
	if _, err = s.Submit(ctx, "bob", sub, "flag{test}"); err != nil {
		t.Fatal("rate leaked to another user", err)
	}
}
func TestEnvironmentBindingAndPackageVersion(t *testing.T) {
	s, ctx := setup(t)
	x := saveDraft(t, s, ctx, draft())
	m := environment.Manifest{FormatVersion: 1, Version: "1.0", Architecture: "amd64", Images: []environment.Image{{Key: "web", File: "images/web.tar", SHA256: strings.Repeat("0", 64)}}, Services: []environment.Service{{Key: "web", Image: "web", MemoryMB: 128, CPU: 1}}}
	if err := s.AttachPackage(ctx, "admin", x.ID, x.Questions[0].ID, x.Version, m, "private.tar", "sha"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(ctx, "admin", x); !errors.Is(err, ErrConflict) {
		t.Fatal("upload lost to stale edit", err)
	}
	x, err := s.Get(ctx, x.ID, "admin", true)
	if err != nil || !x.Questions[0].PackageValidated {
		t.Fatal(err)
	}
	x.Questions[0].Subtasks[0].ServiceKey = "web"
	x = saveDraft(t, s, ctx, x)
	if !x.Questions[0].PackageValidated {
		t.Fatal("unchanged package invalidated")
	}
	if err = s.Transition(ctx, "admin", x.ID, "PUBLISHED", x.Version); err == nil {
		t.Fatal("unverified runtime published")
	}
	x.Questions[0].Subtasks[0].ServiceKey = "missing"
	x = saveDraft(t, s, ctx, x)
	if x.Questions[0].Subtasks[0].ServiceKey != "" {
		t.Fatal("subtask retained container binding")
	}
	x.Questions[0].Subtasks[0].ServiceKey = "web"
	x.Questions[0].Environment.Version = "2.0"
	x.Questions[0].Environment.Services[0].CPU = 2
	x.Questions[0].Environment.Services[0].MemoryMB = 256
	x.Questions[0].Environment.Services[0].ResourceLimitsEnabled = true
	x.Questions[0].Environment.Services[0].PidsLimit = 512
	x.Questions[0].Environment.Services[0].Ports = []environment.Port{{Port: 8080, Protocol: "tcp", Public: true}}
	x = saveDraft(t, s, ctx, x)
	if !x.Questions[0].PackageValidated || x.Questions[0].RuntimeReady {
		t.Fatal("editable runtime configuration lost package or retained runtime approval")
	}
	if !x.Questions[0].Environment.Services[0].ResourceLimitsEnabled || x.Questions[0].Environment.Services[0].PidsLimit != 512 || x.Questions[0].Environment.Services[0].CPU != 2 || x.Questions[0].Environment.Services[0].Ports[0].Port != 8080 {
		t.Fatal("runtime edits not persisted")
	}
	x.Questions[0].Environment.Architecture = "arm64"
	x = saveDraft(t, s, ctx, x)
	if x.Questions[0].PackageValidated {
		t.Fatal("image architecture changed without replacing package")
	}
	// IDs from another draft must never permit stealing or deleting its content.
	other := saveDraft(t, s, ctx, draft())
	x.Questions[0].ID = other.Questions[0].ID
	if _, err = s.Save(ctx, "admin", x); !errors.Is(err, ErrConflict) {
		t.Fatal("cross-set ID reuse", err)
	}
}

func TestRuntimeReleaseBoundToPackageEngineAndSavedContent(t *testing.T) {
	s, ctx := setup(t)
	x := saveDraft(t, s, ctx, draft())
	m := environment.Manifest{FormatVersion: 1, Version: "1", Architecture: "amd64", Images: []environment.Image{{Key: "web", File: "images/web.tar", SHA256: strings.Repeat("0", 64)}}, Services: []environment.Service{{Key: "web", Image: "web", MemoryMB: 64, CPU: 1}}}
	if err := s.AttachPackage(ctx, "admin", x.ID, x.Questions[0].ID, x.Version, m, "private.tar", "sha"); err != nil {
		t.Fatal(err)
	}
	x, err := s.Get(ctx, x.ID, "admin", true)
	if err != nil {
		t.Fatal(err)
	}
	stamp := func(sha, engine string) {
		t.Helper()
		if _, err := s.DB.Exec(`INSERT OR REPLACE INTO runtime_verifications SELECT id,?,environment_json,?,0 FROM challenges WHERE id=?`, sha, engine, x.Questions[0].ID); err != nil {
			t.Fatal(err)
		}
	}
	s.RuntimeEngineID = "engine-current"
	stamp("old-package", "engine-current")
	if err = s.Transition(ctx, "admin", x.ID, "PUBLISHED", x.Version); err == nil {
		t.Fatal("accepted stale package verification")
	}
	stamp("sha", "engine-old")
	if err = s.Transition(ctx, "admin", x.ID, "PUBLISHED", x.Version); err == nil {
		t.Fatal("accepted other engine verification")
	}
	stamp("sha", "engine-current")
	x, err = s.Get(ctx, x.ID, "admin", true)
	if err != nil || !x.Questions[0].RuntimeReady {
		t.Fatal("missing runtime readiness", err)
	}
	// Read-only JSON fields from the client cannot manufacture verification.
	x = saveDraft(t, s, ctx, x)
	if x.Questions[0].RuntimeReady {
		t.Fatal("save retained prior verification")
	}
	if err = s.Transition(ctx, "admin", x.ID, "PUBLISHED", x.Version); err == nil {
		t.Fatal("client runtime_ready bypassed acceptance")
	}
	stamp("sha", "engine-current")
	if err = s.Transition(ctx, "admin", x.ID, "PUBLISHED", x.Version); err != nil {
		t.Fatal("verified release blocked", err)
	}
	public, err := s.Get(ctx, x.ID, "alice", false)
	if err != nil || !public.Questions[0].HasEnvironment || public.Questions[0].Environment != nil {
		t.Fatal("player runtime visibility", err)
	}
}

func TestCopyQuestionPreservesFlags(t *testing.T) {
	s, ctx := setup(t)
	source := saveDraft(t, s, ctx, draft())
	q := source.Questions[0]
	sourceID := q.ID
	q.SourceID = q.ID
	q.ID = ""
	for i := range q.Subtasks {
		q.Subtasks[i].ID = ""
	}
	target := draft()
	target.Questions = []Question{q}
	copied := saveDraft(t, s, ctx, target)
	if copied.Questions[0].ID == sourceID || copied.Questions[0].SourceID != "" {
		t.Fatal("copy must have independent identity")
	}
	copied = publish(t, s, ctx, copied)
	result, err := s.Submit(ctx, "alice", copied.Questions[0].Subtasks[0].ID, "flag{test}")
	if err != nil || !result.Correct {
		t.Fatalf("copied flag failed: %+v %v", result, err)
	}
	original, err := s.Get(ctx, source.ID, "admin", true)
	if err != nil || original.Status != "DRAFT" || original.Questions[0].ID != sourceID {
		t.Fatal("source changed", err)
	}
}

func TestQuestionLibraryIsolationAndCopy(t *testing.T) {
	s, ctx := setup(t)
	input := draft()
	input.Library = true
	library := saveDraft(t, s, ctx, input)
	if !library.Library {
		t.Fatal("library marker missing")
	}
	for _, admin := range []bool{true, false} {
		items, err := s.List(ctx, admin)
		if err != nil || len(items) != 0 {
			t.Fatalf("library leaked into competitions: %v", err)
		}
	}
	items, err := s.ListLibrary(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("library missing: %v", err)
	}
	if _, err = s.Get(ctx, library.ID, "alice", false); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("library exposed to player", err)
	}
	if err = s.Transition(ctx, "admin", library.ID, "PUBLISHED", library.Version); err == nil {
		t.Fatal("library published")
	}
	copyInput := draft()
	q := library.Questions[0]
	q.SourceID, q.ID = q.ID, ""
	for i := range q.Subtasks {
		q.Subtasks[i].ID = ""
		q.Subtasks[i].Flag = ""
	}
	copyInput.Questions = []Question{q}
	copied := saveDraft(t, s, ctx, copyInput)
	if copied.Library || copied.Questions[0].ID == library.Questions[0].ID {
		t.Fatal("copy not independent")
	}
	if err = s.Delete(ctx, "admin", library.ID, library.Version); err != nil {
		t.Fatal(err)
	}
	copied = publish(t, s, ctx, copied)
	result, err := s.Submit(ctx, "alice", copied.Questions[0].Subtasks[0].ID, "flag{test}")
	if err != nil || !result.Correct {
		t.Fatal("copied flag lost", err)
	}
}

func TestDescriptiveAttachmentsPersistAndRemove(t *testing.T) {
	s, ctx := setup(t)
	x := draft()
	x.Questions[0].Attachments = []Attachment{{Name: "说明.txt", Data: "aGVsbG8="}}
	x = saveDraft(t, s, ctx, x)
	if len(x.Questions[0].Attachments) != 1 || x.Questions[0].Attachments[0].Data != "aGVsbG8=" {
		t.Fatal("attachment did not persist")
	}
	x = publish(t, s, ctx, x)
	visible, err := s.Get(ctx, x.ID, "alice", false)
	if err != nil || len(visible.Questions[0].Attachments) != 1 {
		t.Fatal("published attachment unavailable", err)
	}
	y := draft()
	y.Questions[0].Attachments = []Attachment{{Name: "bad.txt", Data: "invalid"}}
	if _, err := s.Save(ctx, "admin", y); err == nil {
		t.Fatal("invalid base64 accepted")
	}
	y.Questions[0].Attachments = []Attachment{{Name: "说明.txt", Data: "aGVsbG8="}}
	y = saveDraft(t, s, ctx, y)
	y.Questions[0].Attachments = nil
	y = saveDraft(t, s, ctx, y)
	if len(y.Questions[0].Attachments) != 0 {
		t.Fatal("attachment removal not persisted")
	}
}

func TestAttachmentsWithoutUploadLimits(t *testing.T) {
	s, ctx := setup(t)
	x := draft()
	for i := 0; i < 11; i++ {
		x.Questions[0].Attachments = append(x.Questions[0].Attachments, Attachment{Name: "empty.txt", Data: ""})
	}
	large := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", 2*1024*1024)))
	x.Questions[0].Attachments = append(x.Questions[0].Attachments, Attachment{Name: strings.Repeat("长", 100) + ".txt", Data: large})
	x = saveDraft(t, s, ctx, x)
	got, err := s.Get(ctx, x.ID, "admin", true)
	if err != nil || len(got.Questions[0].Attachments) != 12 || got.Questions[0].Attachments[11].Data != large {
		t.Fatal("unrestricted attachments did not persist", err)
	}
}

func TestContainerDraftIndependentOfSubtaskFlags(t *testing.T) {
	s, ctx := setup(t)
	input := draft()
	for i := range input.Questions[0].Subtasks {
		input.Questions[0].Subtasks[i].Flag = ""
		input.Questions[0].Subtasks[i].ServiceKey = "obsolete-service"
	}
	x := saveDraft(t, s, ctx, input)
	for _, subtask := range x.Questions[0].Subtasks {
		if subtask.FlagConfigured || subtask.ServiceKey != "" {
			t.Fatal("unconfigured subtask has flag or container binding")
		}
	}
	m := environment.Manifest{FormatVersion: 1, Version: "1.0", Architecture: "amd64", Images: []environment.Image{{Key: "web", File: "images/web.tar", SHA256: strings.Repeat("0", 64)}}, Services: []environment.Service{{Key: "web", Image: "web", MemoryMB: 128, CPU: 1}}}
	// Old persisted bindings must not block replacement packages either.
	if _, err := s.DB.Exec("UPDATE subtasks SET service_key='legacy-service' WHERE challenge_id=?", x.Questions[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AttachPackage(ctx, "admin", x.ID, x.Questions[0].ID, x.Version, m, "private.tar", "sha"); err != nil {
		t.Fatal("container package depends on subtasks", err)
	}
	x, err := s.Get(ctx, x.ID, "admin", true)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Transition(ctx, "admin", x.ID, "PUBLISHED", x.Version); err == nil || !strings.Contains(err.Error(), "Flag") {
		t.Fatal("missing flags did not block publication", err)
	}
	// Removing the environment is independent of subtask configuration.
	x.Questions[0].Environment = nil
	x = saveDraft(t, s, ctx, x)
	for i := range x.Questions[0].Subtasks {
		x.Questions[0].Subtasks[i].Flag = "flag{configured}"
	}
	x = saveDraft(t, s, ctx, x)
	for _, subtask := range x.Questions[0].Subtasks {
		if !subtask.FlagConfigured {
			t.Fatal("configured flag missing")
		}
	}
	publish(t, s, ctx, x)
}

func TestSubtaskResourcesPersistAndValidate(t *testing.T) {
	s, ctx := setup(t)
	x := saveDraft(t, s, ctx, draft())
	m := environment.Manifest{FormatVersion: 1, Version: "1", Architecture: "amd64", Images: []environment.Image{{Key: "web", File: "images/web.tar", SHA256: strings.Repeat("0", 64)}}, Services: []environment.Service{{Key: "web", Image: "web", MemoryMB: 128, CPU: 1}, {Key: "api", Image: "web", MemoryMB: 128, CPU: 1}}}
	if err := s.AttachPackage(ctx, "admin", x.ID, x.Questions[0].ID, x.Version, m, "private.tar", "sha"); err != nil {
		t.Fatal(err)
	}
	x, err := s.Get(ctx, x.ID, "admin", true)
	if err != nil {
		t.Fatal(err)
	}
	x.Questions[0].Subtasks[0].Attachments = []Attachment{{Name: "task.txt", Data: "aGVsbG8="}}
	x.Questions[0].Subtasks[0].ServiceKeys = []string{"web", "api"}
	x = saveDraft(t, s, ctx, x)
	task := x.Questions[0].Subtasks[0]
	if len(task.Attachments) != 1 || task.Attachments[0].Data != "aGVsbG8=" || len(task.ServiceKeys) != 2 {
		t.Fatal("resources did not persist")
	}
	x.Questions[0].Subtasks[0].ServiceKeys = []string{"missing"}
	if _, err := s.Save(ctx, "admin", x); err == nil {
		t.Fatal("unknown service accepted")
	}
	x.Questions[0].Subtasks[0].ServiceKeys = []string{"web", "web"}
	if _, err := s.Save(ctx, "admin", x); err == nil {
		t.Fatal("duplicate service accepted")
	}
	x.Questions[0].Subtasks[0].ServiceKeys = nil
	x.Questions[0].Subtasks[0].Attachments = []Attachment{{Name: "bad.txt", Data: "invalid"}}
	if _, err := s.Save(ctx, "admin", x); err == nil {
		t.Fatal("invalid attachment accepted")
	}
	x.Questions[0].Subtasks[0].Attachments = nil
	x = saveDraft(t, s, ctx, x)
	if len(x.Questions[0].Subtasks[0].Attachments) != 0 || len(x.Questions[0].Subtasks[0].ServiceKeys) != 0 {
		t.Fatal("removal did not persist")
	}
}

func TestEmptySubtasksDraft(t *testing.T) {
	s, ctx := setup(t)
	x := draft()
	x.Questions[0].Subtasks = []Subtask{}
	x = saveDraft(t, s, ctx, x)
	if x.Questions[0].Subtasks == nil || len(x.Questions[0].Subtasks) != 0 {
		t.Fatal("empty draft subtasks must be an array")
	}
	if err := s.Transition(ctx, "admin", x.ID, "PUBLISHED", x.Version); err == nil {
		t.Fatal("empty question published")
	}
}

func TestUpdateCompetitionInfoAfterPublication(t *testing.T) {
	s, ctx := setup(t)
	x := publish(t, s, ctx, saveDraft(t, s, ctx, draft()))
	for _, status := range []string{"PUBLISHED", "CLOSED"} {
		if status == "CLOSED" {
			if err := s.Transition(ctx, "admin", x.ID, status, x.Version); err != nil {
				t.Fatal(err)
			}
			var err error
			x, err = s.Get(ctx, x.ID, "admin", true)
			if err != nil {
				t.Fatal(err)
			}
		}
		oldVersion := x.Version
		oldTask := x.Questions[0].Subtasks[0].ID
		x.Title = "修改名称"
		x.Description = "修改介绍"
		x.EndsAt += 1000
		if err := s.UpdateInfo(ctx, "admin", x.ID, x); err != nil {
			t.Fatal(err)
		}
		got, err := s.Get(ctx, x.ID, "admin", true)
		if err != nil {
			t.Fatal(err)
		}
		if got.Title != x.Title || got.Description != x.Description || got.EndsAt != x.EndsAt || got.Status != status || got.Questions[0].Subtasks[0].ID != oldTask {
			t.Fatal("metadata update changed competition content or failed")
		}
		x.Version = oldVersion
		if err := s.UpdateInfo(ctx, "admin", x.ID, x); err != ErrConflict {
			t.Fatal("stale version accepted", err)
		}
		x = got
	}
	x.EndsAt = x.StartsAt
	if err := s.UpdateInfo(ctx, "admin", x.ID, x); err == nil {
		t.Fatal("invalid dates accepted")
	}
}

func TestPublishAndTakeDownVisibility(t *testing.T) {
	s, ctx := setup(t)
	x := saveDraft(t, s, ctx, draft())
	if _, err := s.Get(ctx, x.ID, "alice", false); err != sql.ErrNoRows {
		t.Fatal("draft visible", err)
	}
	x = publish(t, s, ctx, x)
	if err := s.Transition(ctx, "admin", x.ID, "CLOSED", x.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, x.ID, "alice", false); err != sql.ErrNoRows {
		t.Fatal("unpublished detail visible", err)
	}
	items, err := s.List(ctx, false)
	if err != nil || len(items) != 0 {
		t.Fatal("unpublished list visible", err)
	}
	if _, err := s.Leaderboard(ctx, x.ID, false); err != sql.ErrNoRows {
		t.Fatal("unpublished rankings visible", err)
	}
	if _, err := s.History(ctx, "alice", x.ID); err != sql.ErrNoRows {
		t.Fatal("unpublished history visible", err)
	}
	x, err = s.Get(ctx, x.ID, "admin", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(ctx, "admin", x.ID, "PUBLISHED", x.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, x.ID, "alice", false); err != nil {
		t.Fatal("republished detail hidden", err)
	}
}

func TestChooseQuestionsAfterTakeDownPreservesScores(t *testing.T) {
	s, ctx := setup(t)
	x := publish(t, s, ctx, saveDraft(t, s, ctx, draft()))
	if _, err := s.Submit(ctx, "alice", x.Questions[0].Subtasks[0].ID, "flag{test}"); err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(ctx, "admin", x.ID, "CLOSED", x.Version); err != nil {
		t.Fatal(err)
	}
	x, err := s.Get(ctx, x.ID, "admin", true)
	if err != nil {
		t.Fatal(err)
	}
	extra := draft().Questions[0]
	x.Questions = append(x.Questions, extra)
	x = saveDraft(t, s, ctx, x)
	if len(x.Questions) != 2 || x.Status != "CLOSED" {
		t.Fatal("selection not saved")
	}
	ranks, err := s.Leaderboard(ctx, x.ID, true)
	if err != nil || len(ranks) != 1 {
		t.Fatal("scores lost", err)
	}
	x.Questions = x.Questions[1:]
	if _, err := s.Save(ctx, "admin", x); err == nil {
		t.Fatal("removed question with submissions")
	}
}

func TestLibraryBindingChangesSyncCompetitionCopies(t *testing.T) {
	s, ctx := setup(t)
	source := draft()
	source.Library = true
	source = saveDraft(t, s, ctx, source)
	manifest := environment.Manifest{FormatVersion: 1, Version: "1", Architecture: "amd64", Images: []environment.Image{{Key: "web", File: "images/web.tar", SHA256: strings.Repeat("0", 64)}}, Services: []environment.Service{{Key: "web", Image: "web", MemoryMB: 128, CPU: 1}}}
	if err := s.AttachPackage(ctx, "admin", source.ID, source.Questions[0].ID, source.Version, manifest, "private.tar", "sha"); err != nil {
		t.Fatal(err)
	}
	source, err := s.Get(ctx, source.ID, "admin", true)
	if err != nil {
		t.Fatal(err)
	}
	source.Questions[0].Subtasks[0].ServiceKeys = []string{"web"}
	source = saveDraft(t, s, ctx, source)
	competition := draft()
	var q Question
	encoded, _ := json.Marshal(source.Questions[0])
	if err := json.Unmarshal(encoded, &q); err != nil {
		t.Fatal(err)
	}
	q.SourceID = q.ID
	q.ID = ""
	for i := range q.Subtasks {
		q.Subtasks[i].ID = ""
	}
	competition.Questions = []Question{q}
	competition = saveDraft(t, s, ctx, competition)
	// A normal later save must preserve source links.
	competition = saveDraft(t, s, ctx, competition)
	source.Questions[0].Subtasks[0].ServiceKeys = nil
	source = saveDraft(t, s, ctx, source)
	got, err := s.Get(ctx, competition.ID, "admin", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Questions[0].Subtasks[0].ServiceKeys) != 0 {
		t.Fatal("deleted binding still present")
	}
	source.Questions[0].Subtasks[0].ServiceKeys = []string{"web"}
	source = saveDraft(t, s, ctx, source)
	got, err = s.Get(ctx, competition.ID, "admin", true)
	if err != nil || len(got.Questions[0].Subtasks[0].ServiceKeys) != 1 {
		t.Fatal("binding restore not synchronized", err)
	}
}

func TestSolutionDocumentPersistenceAndPrivacy(t *testing.T) {
	s, ctx := setup(t)
	x := draft()
	doc := &Attachment{Name: "solution.pdf", Data: base64.StdEncoding.EncodeToString([]byte("%PDF-1.7\nsolution"))}
	x.Questions[0].SolutionDocument = doc
	x = saveDraft(t, s, ctx, x)
	if got := x.Questions[0].SolutionDocument; got == nil || *got != *doc {
		t.Fatal("solution document not persisted", got)
	}
	x = publish(t, s, ctx, x)
	player, err := s.Get(ctx, x.ID, "alice", false)
	if err != nil {
		t.Fatal(err)
	}
	if player.Questions[0].SolutionDocument != nil {
		t.Fatal("solution leaked to player")
	}
	y := draft()
	y.Questions[0].SolutionDocument = &Attachment{Name: "fake.pdf", Data: base64.StdEncoding.EncodeToString([]byte("not a PDF"))}
	if _, err := s.Save(ctx, "admin", y); err == nil {
		t.Fatal("invalid PDF accepted")
	}
	z := draft()
	z.Questions[0].SolutionDocument = doc
	z = saveDraft(t, s, ctx, z)
	z.Questions[0].SolutionDocument = nil
	z = saveDraft(t, s, ctx, z)
	if z.Questions[0].SolutionDocument != nil {
		t.Fatal("solution document not removed")
	}
}
