package competition

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"huashan/internal/auth"
	"huashan/internal/environment"
)

var ErrConflict = errors.New("内容已变化或已发布，请刷新后重试；已发布内容不可修改")
var ErrClosed = errors.New("当前不在允许提交的时间范围内")
var ErrRate = errors.New("提交过于频繁，请一分钟后重试")

type ValidationError struct{ Message string }

func solutionDocumentJSON(a *Attachment) string { b, _ := json.Marshal(a); return string(b) }
func attachmentJSON(a []Attachment) string      { b, _ := json.Marshal(a); return string(b) }
func (e *ValidationError) Error() string        { return e.Message }
func invalid(message string) error              { return &ValidationError{message} }

type Set struct {
	Library     bool       `json:"library"`
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Status      string     `json:"status"`
	StartsAt    int64      `json:"starts_at"`
	EndsAt      int64      `json:"ends_at"`
	Version     int        `json:"version"`
	Questions   []Question `json:"questions"`
}
type Attachment struct {
	Name   string `json:"name"`
	Data   string `json:"data"`
	FileID string `json:"file_id,omitempty"`
	Size   int64  `json:"size,omitempty"`
}
type Question struct {
	SolutionDocument *Attachment           `json:"solution_document,omitempty"`
	Attachments      []Attachment          `json:"attachments,omitempty"`
	SourceID         string                `json:"source_id,omitempty"`
	ID               string                `json:"id"`
	Title            string                `json:"title"`
	Description      string                `json:"description"`
	Tags             []string              `json:"tags"`
	Category         string                `json:"category"`
	Difficulty       string                `json:"difficulty"`
	Environment      *environment.Manifest `json:"environment"`
	PackageValidated bool                  `json:"package_validated"`
	PackageSHA256    string                `json:"package_sha256,omitempty"`
	PackageToken     string                `json:"package_token,omitempty"`
	RuntimeReady     bool                  `json:"runtime_ready"`
	HasEnvironment   bool                  `json:"has_environment"`
	Subtasks         []Subtask             `json:"subtasks"`
}
type Subtask struct {
	Attachments    []Attachment `json:"attachments,omitempty"`
	ServiceKeys    []string     `json:"service_keys,omitempty"`
	ID             string       `json:"id"`
	Title          string       `json:"title"`
	Content        string       `json:"content"`
	Points         int          `json:"points"`
	ServiceKey     string       `json:"service_key"`
	Flag           string       `json:"flag,omitempty"`
	FlagConfigured bool         `json:"flag_configured"`
	Solved         bool         `json:"solved"`
	Attempts       int          `json:"attempts"`
}
type Service struct {
	DB              *sql.DB
	RuntimeEngineID string
}

func (s *Service) List(ctx context.Context, admin bool) ([]Set, error) {
	return s.list(ctx, admin, false)
}
func (s *Service) ListLibrary(ctx context.Context) ([]Set, error) {
	return s.list(ctx, true, true)
}
func (s *Service) list(ctx context.Context, admin, library bool) ([]Set, error) {
	query := "SELECT id,title,description,status,starts_at,ends_at,version FROM challenge_sets WHERE "
	if !library {
		query += "NOT "
	}
	query += "EXISTS (SELECT 1 FROM question_library WHERE set_id=challenge_sets.id)"
	if !admin {
		query += " AND status='PUBLISHED'"
	}
	query += " ORDER BY created_at DESC,id"
	rows, err := s.DB.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Set{}
	for rows.Next() {
		var x Set
		if err = rows.Scan(&x.ID, &x.Title, &x.Description, &x.Status, &x.StartsAt, &x.EndsAt, &x.Version); err != nil {
			return nil, err
		}
		x.Library = library
		x.Questions = []Question{}
		items = append(items, x)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if library {
		metadata, e := s.DB.QueryContext(ctx, "SELECT c.set_id,c.id,c.title,c.category,c.difficulty,c.tags_json FROM challenges c JOIN question_library l ON l.set_id=c.set_id")
		if e != nil {
			return nil, e
		}
		defer metadata.Close()
		byID := map[string]int{}
		for i := range items {
			byID[items[i].ID] = i
		}
		for metadata.Next() {
			var id, tags string
			var q Question
			if e = metadata.Scan(&id, &q.ID, &q.Title, &q.Category, &q.Difficulty, &tags); e != nil {
				return nil, e
			}
			if e = json.Unmarshal([]byte(tags), &q.Tags); e != nil {
				return nil, e
			}
			if i, ok := byID[id]; ok {
				items[i].Questions = append(items[i].Questions, q)
			}
		}
		if e = metadata.Err(); e != nil {
			return nil, e
		}
	}
	return items, nil
}
func (s *Service) Get(ctx context.Context, id, user string, admin bool) (Set, error) {
	var x Set
	if err := s.syncBindings(ctx); err != nil {
		return x, err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return x, err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, "SELECT id,title,description,status,starts_at,ends_at,version FROM challenge_sets WHERE id=?", id).Scan(&x.ID, &x.Title, &x.Description, &x.Status, &x.StartsAt, &x.EndsAt, &x.Version)
	if err != nil {
		return x, err
	}
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM question_library WHERE set_id=?)", id).Scan(&x.Library); err != nil {
		return x, err
	}
	if !admin && (x.Status != "PUBLISHED" || x.Library) {
		return Set{}, sql.ErrNoRows
	}
	x.Questions = []Question{}
	rows, err := tx.QueryContext(ctx, "SELECT id,title,description,category,difficulty,environment_json,package_sha256,attachments_json,solution_document_json,tags_json FROM challenges WHERE set_id=? ORDER BY position,id", id)
	if err != nil {
		return x, err
	}
	for rows.Next() {
		var q Question
		var env, attachments, solution, tags string
		if err = rows.Scan(&q.ID, &q.Title, &q.Description, &q.Category, &q.Difficulty, &env, &q.PackageSHA256, &attachments, &solution, &tags); err != nil {
			rows.Close()
			return x, err
		}
		if err = json.Unmarshal([]byte(env), &q.Environment); err != nil {
			rows.Close()
			return x, err
		}
		if err = json.Unmarshal([]byte(tags), &q.Tags); err != nil {
			rows.Close()
			return x, err
		}
		if q.Environment != nil {
			q.Environment.AssignServiceIDs(q.PackageSHA256)
		}
		if err = json.Unmarshal([]byte(attachments), &q.Attachments); err != nil {
			rows.Close()
			return x, err
		}
		if admin {
			if err = json.Unmarshal([]byte(solution), &q.SolutionDocument); err != nil {
				rows.Close()
				return x, err
			}
		}
		q.PackageValidated = q.PackageSHA256 != ""
		q.Subtasks = []Subtask{}
		x.Questions = append(x.Questions, q)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return x, err
	}
	for i := range x.Questions {
		q := &x.Questions[i]
		q.Subtasks = []Subtask{}
		q.HasEnvironment = q.Environment != nil
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM runtime_verifications v JOIN challenges q ON q.id=v.challenge_id WHERE q.id=? AND v.package_sha256=q.package_sha256 AND v.environment_json=q.environment_json)`, q.ID).Scan(&q.RuntimeReady); err != nil {
			return x, err
		}
		rows, err = tx.QueryContext(ctx, `SELECT t.id,t.title,t.content,t.points,'',COALESCE(r.attachments_json,'[]'),COALESCE(r.service_keys_json,'[]'),t.flag_hash!='',
   EXISTS(SELECT 1 FROM solves so WHERE so.subtask_id=t.id AND so.user_id=?),
   (SELECT count(*) FROM submissions su WHERE su.subtask_id=t.id AND su.user_id=?)
   FROM subtasks t LEFT JOIN subtask_resources r ON r.subtask_id=t.id WHERE challenge_id=? ORDER BY position,id`, user, user, q.ID)
		if err != nil {
			return x, err
		}
		for rows.Next() {
			var t Subtask
			var attachments, services string
			if err = rows.Scan(&t.ID, &t.Title, &t.Content, &t.Points, &t.ServiceKey, &attachments, &services, &t.FlagConfigured, &t.Solved, &t.Attempts); err != nil {
				rows.Close()
				return x, err
			}
			if err = json.Unmarshal([]byte(attachments), &t.Attachments); err != nil {
				rows.Close()
				return x, err
			}
			if err = json.Unmarshal([]byte(services), &t.ServiceKeys); err != nil {
				rows.Close()
				return x, err
			}
			t.FlagConfigured = admin && t.FlagConfigured
			q.Subtasks = append(q.Subtasks, t)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return x, err
		}
		// Runtime topology and image details are administrator-only.
		if !admin {
			q.Environment = nil
			q.PackageSHA256 = ""
			q.PackageValidated = false
		}
	}
	return x, tx.Commit()
}
func validAttachment(a Attachment) bool {
	if strings.TrimSpace(a.Name) == "" {
		return false
	}
	if a.FileID != "" {
		if len(a.FileID) != 64 || a.Data != "" || a.Size <= 0 {
			return false
		}
		_, err := hex.DecodeString(a.FileID)
		return err == nil
	}
	_, err := base64.StdEncoding.DecodeString(a.Data)
	return err == nil
}

func validate(x *Set) error {
	x.Title = strings.TrimSpace(x.Title)
	if x.Title == "" || len(x.Title) > 200 || len(x.Description) > 16000 || x.StartsAt < 0 || x.EndsAt <= x.StartsAt || x.EndsAt > 253402300799000 || len(x.Questions) > 50 {
		return invalid("套题名称、描述、时间范围或题目数量无效（最多 50 题）")
	}
	ids := map[string]bool{}
	for i := range x.Questions {
		q := &x.Questions[i]
		if doc := q.SolutionDocument; doc != nil {
			data, err := base64.StdEncoding.DecodeString(doc.Data)
			if err != nil || !strings.HasSuffix(strings.ToLower(doc.Name), ".pdf") || len(data) > 20*1024*1024 || !strings.HasPrefix(string(data), "%PDF-") {
				return invalid("解题文档须为有效的 PDF 文件，且不超过 20 MiB")
			}
		}
		q.Title = strings.TrimSpace(q.Title)
		q.Category = strings.TrimSpace(q.Category)
		if len(q.Tags) > 20 {
			return invalid("每道题最多添加 20 个考点标签")
		}
		tags := []string{}
		seen := map[string]bool{}
		for _, tag := range q.Tags {
			tag = strings.TrimSpace(tag)
			if tag == "" || len([]rune(tag)) > 32 {
				return invalid("考点标签须为 1 至 32 个字符")
			}
			key := strings.ToLower(tag)
			if !seen[key] {
				tags = append(tags, tag)
				seen[key] = true
			}
		}
		q.Tags = tags
		for _, a := range q.Attachments {
			if !validAttachment(a) {
				return invalid("附件名称或内容无效")
			}
		}
		if q.Title == "" || len(q.Title) > 200 || len(q.Description) > 32000 || len(q.Category) > 64 || len(q.Subtasks) > 50 {
			return invalid("每题须填写名称，小题最多 50 个，且内容不得超长")
		}
		if q.Difficulty == "" || len(q.Difficulty) > 64 {
			return invalid("请选择有效的难度")
		}
		if q.ID == "" {
			q.ID = auth.RandomToken()
		}
		if ids[q.ID] || len(q.ID) != 64 {
			return invalid("题目或小题标识无效/重复")
		}
		ids[q.ID] = true
		if q.Environment != nil {
			if err := q.Environment.Validate(); err != nil {
				return invalid(err.Error())
			}
		}
		for j := range q.Subtasks {
			t := &q.Subtasks[j]
			t.Title = strings.TrimSpace(t.Title)
			if t.ID == "" {
				t.ID = auth.RandomToken()
			}
			if ids[t.ID] || len(t.ID) != 64 {
				return invalid("题目或小题标识无效/重复")
			}
			ids[t.ID] = true
			if t.Title == "" || len(t.Title) > 200 || len(t.Content) > 16000 || t.Points < 1 || t.Points > 100000 || len(t.Flag) > 512 {
				return invalid("小题名称、内容、分值或 Flag 无效")
			}
			for _, a := range t.Attachments {
				if !validAttachment(a) {
					return invalid("小题附件名称或内容无效")
				}
			}
			seen := map[string]bool{}
			for _, key := range t.ServiceKeys {
				found := false
				if q.Environment != nil {
					for _, service := range q.Environment.Services {
						if service.Key == key {
							found = true
							break
						}
					}
				}
				if !found || seen[key] {
					return invalid("小题绑定的 Docker 服务不存在或重复")
				}
				seen[key] = true
			}
			// Deprecated compatibility field: containers belong to the question.
			t.ServiceKey = ""
		}
	}
	return nil
}
func digest(key, flag string) string {
	h := hmac.New(sha256.New, []byte(key))
	h.Write([]byte(flag))
	return hex.EncodeToString(h.Sum(nil))
}
func audit(ctx context.Context, tx *sql.Tx, actor, action, id string) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO audit_logs VALUES(?,?,?,?,?)", auth.RandomToken(), actor, action, id, time.Now().UnixMilli())
	return err
}
func (s *Service) Save(ctx context.Context, actor string, x Set) (string, error) {
	if x.Library && len(x.Questions) != 1 {
		return "", invalid("题库条目必须包含一道题目")
	}
	if err := validate(&x); err != nil {
		return "", err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	for _, q := range x.Questions {
		var exists int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM difficulties WHERE code=?", q.Difficulty).Scan(&exists); err != nil {
			return "", err
		}
		if exists == 0 {
			return "", invalid("所选难度不存在，请刷新后重新选择")
		}
	}
	var key string
	if err = tx.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='flag_hmac_key'").Scan(&key); err != nil {
		return "", err
	}
	for qi := range x.Questions {
		q := &x.Questions[qi]
		groups := [][]Attachment{q.Attachments}
		for _, task := range q.Subtasks {
			groups = append(groups, task.Attachments)
		}
		for _, group := range groups {
			for i := range group {
				a := &group[i]
				if a.FileID == "" {
					continue
				}
				var name string
				var size int64
				err := tx.QueryRowContext(ctx, "SELECT name,size FROM attachment_uploads WHERE id=? AND state='done'", a.FileID).Scan(&name, &size)
				if errors.Is(err, sql.ErrNoRows) {
					return "", invalid("附件尚未上传完成或不存在，请重新上传")
				}
				if err != nil {
					return "", err
				}
				a.Name = name
				a.Size = size
			}
		}
	}
	oldQuestionSources := map[string]string{}
	oldTaskSources := map[string]string{}
	closedEdit := false
	retained := map[string]bool{}
	oldFlags := map[string]string{}
	type oldPackage struct{ env, path, sha string }
	oldPackages := map[string]oldPackage{}
	if x.ID == "" {
		x.ID = auth.RandomToken()
		_, err = tx.ExecContext(ctx, "INSERT INTO challenge_sets VALUES(?,?,?,'DRAFT',?,?,1,?,?)", x.ID, x.Title, x.Description, x.StartsAt, x.EndsAt, time.Now().UnixMilli(), time.Now().UnixMilli())
	} else {
		var library bool
		if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM question_library WHERE set_id=?)", x.ID).Scan(&library); err != nil {
			return "", err
		}
		if library != x.Library {
			return "", ErrConflict
		}
		var status string
		var version int
		if err = tx.QueryRowContext(ctx, "SELECT status,version FROM challenge_sets WHERE id=?", x.ID).Scan(&status, &version); err != nil {
			return "", err
		}
		if (status != "DRAFT" && status != "CLOSED") || version != x.Version {
			return "", ErrConflict
		}
		for _, spec := range []struct {
			query  string
			values map[string]string
		}{
			{"SELECT link.challenge_id,link.source_id FROM question_sources link JOIN challenges q ON q.id=link.challenge_id WHERE q.set_id=?", oldQuestionSources},
			{"SELECT link.subtask_id,link.source_id FROM subtask_sources link JOIN subtasks t ON t.id=link.subtask_id JOIN challenges q ON q.id=t.challenge_id WHERE q.set_id=?", oldTaskSources},
		} {
			rows, e := tx.QueryContext(ctx, spec.query, x.ID)
			if e != nil {
				return "", e
			}
			for rows.Next() {
				var id, source string
				if e = rows.Scan(&id, &source); e != nil {
					rows.Close()
					return "", e
				}
				spec.values[id] = source
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return "", e
			}
		}
		closedEdit = status == "CLOSED"
		rows, e := tx.QueryContext(ctx, "SELECT t.id,t.flag_hash FROM subtasks t JOIN challenges q ON q.id=t.challenge_id WHERE q.set_id=?", x.ID)
		if e != nil {
			return "", e
		}
		for rows.Next() {
			var id, h string
			if e = rows.Scan(&id, &h); e != nil {
				rows.Close()
				return "", e
			}
			oldFlags[id] = h
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return "", e
		}
		rows, e = tx.QueryContext(ctx, "SELECT id,environment_json,package_path,package_sha256 FROM challenges WHERE set_id=?", x.ID)
		if e != nil {
			return "", e
		}
		for rows.Next() {
			var id string
			var p oldPackage
			if e = rows.Scan(&id, &p.env, &p.path, &p.sha); e != nil {
				rows.Close()
				return "", e
			}
			oldPackages[id] = p
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return "", e
		}
		_, err = tx.ExecContext(ctx, "UPDATE challenge_sets SET title=?,description=?,starts_at=?,ends_at=?,version=version+1,updated_at=? WHERE id=?", x.Title, x.Description, x.StartsAt, x.EndsAt, time.Now().UnixMilli(), x.ID)
		if err == nil {
			if !closedEdit {
				_, err = tx.ExecContext(ctx, "DELETE FROM challenges WHERE set_id=?", x.ID)
			} else {
				chosen := map[string]bool{}
				for _, q := range x.Questions {
					chosen[q.ID] = true
				}
				for id := range oldPackages {
					if chosen[id] {
						retained[id] = true
						continue
					}
					var attempts int
					if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM submissions su JOIN subtasks t ON t.id=su.subtask_id WHERE t.challenge_id=?", id).Scan(&attempts); err != nil {
						return "", err
					}
					if attempts > 0 {
						return "", invalid("该题已有提交记录，不能移除")
					}
					if _, err = tx.ExecContext(ctx, "DELETE FROM challenges WHERE id=? AND set_id=?", id, x.ID); err != nil {
						return "", err
					}
				}
			}
		}
	}
	if err != nil {
		return "", err
	}
	if x.Library {
		if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO question_library(set_id) VALUES(?)", x.ID); err != nil {
			return "", err
		}
	}
	for i, q := range x.Questions {
		for ti, tag := range q.Tags {
			if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO knowledge_points(name) VALUES(?)", tag); err != nil {
				return "", err
			}
			if err = tx.QueryRowContext(ctx, "SELECT name FROM knowledge_points WHERE name=?", tag).Scan(&q.Tags[ti]); err != nil {
				return "", err
			}
		}
		if q.Category != "" {
			if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO question_types(name) VALUES(?)", q.Category); err != nil {
				return "", err
			}
			if err = tx.QueryRowContext(ctx, "SELECT name FROM question_types WHERE name=?", q.Category).Scan(&q.Category); err != nil {
				return "", err
			}
		}
		if closedEdit && retained[q.ID] {
			if _, err = tx.ExecContext(ctx, "UPDATE challenges SET position=? WHERE id=? AND set_id=?", i, q.ID, x.ID); err != nil {
				return "", err
			}
			continue
		}
		// Reject client-supplied IDs belonging to a different set before INSERT.
		var count int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM challenges WHERE id=?", q.ID).Scan(&count); err != nil {
			return "", err
		}
		if count != 0 {
			return "", ErrConflict
		}
		env, _ := json.Marshal(q.Environment)
		p := oldPackages[q.ID]
		sourceFlags := []string{}
		sourceTasks := []string{}
		if q.SourceID != "" {
			if err = tx.QueryRowContext(ctx, "SELECT environment_json,package_path,package_sha256 FROM challenges WHERE id=?", q.SourceID).Scan(&p.env, &p.path, &p.sha); err != nil {
				return "", err
			}
			rows, e := tx.QueryContext(ctx, "SELECT id,flag_hash FROM subtasks WHERE challenge_id=? ORDER BY position,id", q.SourceID)
			if e != nil {
				return "", e
			}
			for rows.Next() {
				var sourceTask, h string
				if e = rows.Scan(&sourceTask, &h); e != nil {
					rows.Close()
					return "", e
				}
				sourceTasks = append(sourceTasks, sourceTask)
				sourceFlags = append(sourceFlags, h)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return "", e
			}
			if len(sourceFlags) != len(q.Subtasks) {
				return "", invalid("所选题目已变化，请重新选择")
			}
		}
		if q.PackageToken != "" {
			err = tx.QueryRowContext(ctx, "SELECT environment_json,package_path,package_sha256 FROM staged_packages WHERE token=? AND actor_id=? AND expires_at>?", q.PackageToken, actor, time.Now().UnixMilli()).Scan(&p.env, &p.path, &p.sha)
			if errors.Is(err, sql.ErrNoRows) {
				return "", invalid("暂存环境包不存在或已过期，请重新上传")
			}
			if err != nil {
				return "", err
			}
			if q.Environment == nil || !packageConfigurationMatches(p.env, q.Environment) {
				return "", invalid("环境配置与上传包不一致，请重新上传")
			}
			if _, err = tx.ExecContext(ctx, "DELETE FROM staged_packages WHERE token=?", q.PackageToken); err != nil {
				return "", err
			}
		}
		if !packageConfigurationMatches(p.env, q.Environment) {
			p = oldPackage{}
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO challenges VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)", q.ID, x.ID, q.Title, q.Description, q.Category, q.Difficulty, i, string(env), p.path, p.sha, attachmentJSON(q.Attachments), solutionDocumentJSON(q.SolutionDocument), tagsJSON(q.Tags))
		if err != nil {
			return "", err
		}
		if q.SourceID != "" {
			if _, err = tx.ExecContext(ctx, "INSERT INTO question_sources VALUES(?,?)", q.ID, q.SourceID); err != nil {
				return "", err
			}
		}
		if source := oldQuestionSources[q.ID]; q.SourceID == "" && source != "" {
			if _, err = tx.ExecContext(ctx, "INSERT INTO question_sources VALUES(?,?)", q.ID, source); err != nil {
				return "", err
			}
		}
		for j, t := range q.Subtasks {
			h := oldFlags[t.ID]
			if q.SourceID != "" {
				h = sourceFlags[j]
			}
			if t.Flag != "" {
				h = digest(key, t.Flag)
			}
			if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM subtasks WHERE id=?", t.ID).Scan(&count); err != nil {
				return "", err
			}
			if count != 0 {
				return "", ErrConflict
			}
			if _, err = tx.ExecContext(ctx, "INSERT INTO subtasks VALUES(?,?,?,?,?,?,?,?)", t.ID, q.ID, t.Title, t.Content, t.Points, t.ServiceKey, h, j); err != nil {
				return "", err
			}
			if q.SourceID != "" {
				if _, err = tx.ExecContext(ctx, "INSERT INTO subtask_sources VALUES(?,?)", t.ID, sourceTasks[j]); err != nil {
					return "", err
				}
			}
			if source := oldTaskSources[t.ID]; q.SourceID == "" && source != "" {
				if _, err = tx.ExecContext(ctx, "INSERT INTO subtask_sources VALUES(?,?)", t.ID, source); err != nil {
					return "", err
				}
			}
			services, _ := json.Marshal(t.ServiceKeys)
			if _, err = tx.ExecContext(ctx, "INSERT INTO subtask_resources VALUES(?,?,?)", t.ID, attachmentJSON(t.Attachments), string(services)); err != nil {
				return "", err
			}

		}
	}
	if err = syncSubtaskBindings(ctx, tx); err != nil {
		return "", err
	}
	if err = audit(ctx, tx, actor, "set.save", x.ID); err != nil {
		return "", err
	}
	return x.ID, tx.Commit()
}
func (s *Service) Transition(ctx context.Context, actor, id, target string, version int) error {
	if target != "PUBLISHED" && target != "CLOSED" {
		return invalid("发布状态无效")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current string
	var actual int
	var end int64
	if err = tx.QueryRowContext(ctx, "SELECT status,version,ends_at FROM challenge_sets WHERE id=?", id).Scan(&current, &actual, &end); err != nil {
		return err
	}
	if actual != version || (target == "PUBLISHED" && current != "DRAFT" && current != "CLOSED") || (target == "CLOSED" && current != "PUBLISHED") {
		return ErrConflict
	}
	var library bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM question_library WHERE set_id=?)", id).Scan(&library); err != nil {
		return err
	}
	if library {
		return invalid("题库题目不能作为竞赛发布")
	}
	if target == "PUBLISHED" {
		if end <= time.Now().UnixMilli() {
			return invalid("结束时间已经过去，不能发布")
		}
		var n int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM challenges WHERE set_id=?", id).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return invalid("套题至少需要一道题目")
		}
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM challenges q WHERE q.set_id=? AND NOT EXISTS (SELECT 1 FROM subtasks t WHERE t.challenge_id=q.id)", id).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return invalid("发布前每道题目至少需要一个小题")
		}
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM subtasks t JOIN challenges q ON q.id=t.challenge_id WHERE q.set_id=? AND t.flag_hash=''`, id).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return invalid("发布前请为所有小题设置非空 Flag")
		}
		// Container-based releases require runtime validation, not just package parsing.
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM challenges q WHERE q.set_id=? AND q.environment_json!='null' AND NOT EXISTS (SELECT 1 FROM runtime_verifications v WHERE v.challenge_id=q.id AND v.package_sha256=q.package_sha256 AND v.environment_json=q.environment_json AND v.engine_id=? AND q.package_sha256!='')`, id, s.RuntimeEngineID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return invalid("容器题尚未通过当前 Docker Engine 的运行验收；请先保存修改，并对每道容器题执行「运行验收」，通过后再发布")
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE challenge_sets SET status=?,version=version+1,updated_at=? WHERE id=?", target, time.Now().UnixMilli(), id); err != nil {
		return err
	}
	if err = audit(ctx, tx, actor, "set."+strings.ToLower(target), id); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Service) Delete(ctx context.Context, actor, id string, version int) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := tx.ExecContext(ctx, "DELETE FROM challenge_sets WHERE id=? AND status='DRAFT' AND version=?", id, version)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	if err = audit(ctx, tx, actor, "set.delete", id); err != nil {
		return err
	}
	return tx.Commit()
}

type SubmitResult struct {
	Correct       bool `json:"correct"`
	FirstSolve    bool `json:"first_solve"`
	AwardedPoints int  `json:"awarded_points"`
}

func (s *Service) Submit(ctx context.Context, user, subtask, flag string) (SubmitResult, error) {
	var result SubmitResult
	if len(flag) == 0 || len(flag) > 512 {
		return result, invalid("Flag 长度须为 1–512 字节")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	var hash, key, status string
	var points, n int
	var start, end int64
	if err = tx.QueryRowContext(ctx, `SELECT t.flag_hash,t.points,c.status,c.starts_at,c.ends_at FROM subtasks t JOIN challenges q ON q.id=t.challenge_id JOIN challenge_sets c ON c.id=q.set_id WHERE t.id=? AND c.status='PUBLISHED'`, subtask).Scan(&hash, &points, &status, &start, &end); err != nil {
		return result, err
	}
	now := time.Now().UnixMilli()
	if status != "PUBLISHED" || now < start || now >= end {
		return result, ErrClosed
	}
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM submissions WHERE user_id=? AND created_at>?", user, now-60000).Scan(&n); err != nil {
		return result, err
	}
	if n >= 20 {
		return result, ErrRate
	}
	if err = tx.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='flag_hmac_key'").Scan(&key); err != nil {
		return result, err
	}
	result.Correct = hmac.Equal([]byte(hash), []byte(digest(key, flag)))
	submission := auth.RandomToken()
	if _, err = tx.ExecContext(ctx, "INSERT INTO submissions VALUES(?,?,?,?,?)", submission, user, subtask, result.Correct, now); err != nil {
		return result, err
	}
	if result.Correct {
		r, e := tx.ExecContext(ctx, "INSERT INTO solves VALUES(?,?,?,?,?) ON CONFLICT(user_id,subtask_id) DO NOTHING", user, subtask, submission, points, now)
		if e != nil {
			return result, e
		}
		affected, _ := r.RowsAffected()
		result.FirstSolve = affected == 1
		if result.FirstSolve {
			result.AwardedPoints = points
		}
	}
	return result, tx.Commit()
}

type Rank struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
	Points      int    `json:"points"`
	Solved      int    `json:"solved"`
	LastSolve   int64  `json:"last_solve"`
}

func (s *Service) Leaderboard(ctx context.Context, id string, admin bool) ([]Rank, error) {
	var status string
	if err := s.DB.QueryRowContext(ctx, "SELECT status FROM challenge_sets WHERE id=?", id).Scan(&status); err != nil {
		return nil, err
	}
	if !admin && status != "PUBLISHED" {
		return nil, sql.ErrNoRows
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT u.id,u.display_name,sum(so.points),count(*),max(so.solved_at) FROM solves so JOIN users u ON u.id=so.user_id JOIN subtasks t ON t.id=so.subtask_id JOIN challenges q ON q.id=t.challenge_id WHERE q.set_id=? GROUP BY u.id,u.display_name ORDER BY sum(so.points) DESC,max(so.solved_at),u.id LIMIT 500`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Rank{}
	for rows.Next() {
		var r Rank
		if err = rows.Scan(&r.UserID, &r.DisplayName, &r.Points, &r.Solved, &r.LastSolve); err != nil {
			return nil, err
		}
		items = append(items, r)
	}
	return items, rows.Err()
}

type Submission struct {
	ID        string `json:"id"`
	SubtaskID string `json:"subtask_id"`
	Title     string `json:"title"`
	Correct   bool   `json:"correct"`
	CreatedAt int64  `json:"created_at"`
}

func (s *Service) History(ctx context.Context, user, set string) ([]Submission, error) {
	var status string
	if err := s.DB.QueryRowContext(ctx, "SELECT status FROM challenge_sets WHERE id=? AND status='PUBLISHED'", set).Scan(&status); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT su.id,su.subtask_id,t.title,su.correct,su.created_at FROM submissions su JOIN subtasks t ON t.id=su.subtask_id JOIN challenges q ON q.id=t.challenge_id WHERE su.user_id=? AND q.set_id=? ORDER BY su.created_at DESC,su.id DESC LIMIT 100`, user, set)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Submission{}
	for rows.Next() {
		var x Submission
		if err = rows.Scan(&x.ID, &x.SubtaskID, &x.Title, &x.Correct, &x.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, x)
	}
	return items, rows.Err()
}

// AttachPackage uses the same optimistic version as draft editing. The upload
// cannot overwrite a newer edit, nor attach a package to a published set.
func (s *Service) AttachPackage(ctx context.Context, actor, set, question string, version int, m environment.Manifest, path, sha string) error {
	if err := m.Validate(); err != nil {
		return invalid(err.Error())
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := tx.ExecContext(ctx, "UPDATE challenge_sets SET version=version+1,updated_at=? WHERE id=? AND status='DRAFT' AND version=?", time.Now().UnixMilli(), set, version)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	data, _ := json.Marshal(m)
	r, err = tx.ExecContext(ctx, "UPDATE challenges SET environment_json=?,package_path=?,package_sha256=? WHERE id=? AND set_id=?", string(data), path, sha, question, set)
	if err != nil {
		return err
	}
	n, _ = r.RowsAffected()
	if n != 1 {
		return sql.ErrNoRows
	}
	if err = audit(ctx, tx, actor, "environment.package.validated", question); err != nil {
		return err
	}
	return tx.Commit()
}

func packageConfigurationMatches(raw string, current *environment.Manifest) bool {
	if current == nil {
		return raw == "null" || raw == ""
	}
	var original environment.Manifest
	return json.Unmarshal([]byte(raw), &original) == nil && environment.SamePackageImages(original, *current)
}

// UpdateInfo changes competition metadata without replacing questions or scores.
func (s *Service) UpdateInfo(ctx context.Context, actor, id string, input Set) error {
	input.Questions = nil
	if err := validate(&input); err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE challenge_sets SET title=?,description=?,starts_at=?,ends_at=?,version=version+1,updated_at=? WHERE id=? AND version=? AND NOT EXISTS (SELECT 1 FROM question_library WHERE set_id=?)`, input.Title, input.Description, input.StartsAt, input.EndsAt, time.Now().UnixMilli(), id, input.Version, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	if err = audit(ctx, tx, actor, "set.info", id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) syncBindings(ctx context.Context) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := syncSubtaskBindings(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}
func syncSubtaskBindings(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT r.subtask_id,t.challenge_id,q.set_id,
 COALESCE((SELECT json_group_array(k.value) FROM json_each(src.service_keys_json) k
 WHERE EXISTS (SELECT 1 FROM json_each(q.environment_json,'$.services') service WHERE json_extract(service.value,'$.key')=k.value)), '[]') AS bindings
 FROM subtask_sources link JOIN subtask_resources r ON r.subtask_id=link.subtask_id
 JOIN subtask_resources src ON src.subtask_id=link.source_id
 JOIN subtasks t ON t.id=r.subtask_id JOIN challenges q ON q.id=t.challenge_id
 JOIN subtasks st ON st.id=link.source_id JOIN challenges sq ON sq.id=st.challenge_id
 JOIN question_library library ON library.set_id=sq.set_id
 WHERE COALESCE(r.service_keys_json,'null') != COALESCE((SELECT json_group_array(k.value) FROM json_each(src.service_keys_json) k
 WHERE EXISTS (SELECT 1 FROM json_each(q.environment_json,'$.services') service WHERE json_extract(service.value,'$.key')=k.value)), '[]')`)
	if err != nil {
		return err
	}
	type change struct{ task, question, set, bindings string }
	changes := []change{}
	for rows.Next() {
		var c change
		if err := rows.Scan(&c.task, &c.question, &c.set, &c.bindings); err != nil {
			rows.Close()
			return err
		}
		changes = append(changes, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	sets := map[string]bool{}
	for _, c := range changes {
		if _, err := tx.ExecContext(ctx, "UPDATE subtask_resources SET service_keys_json=? WHERE subtask_id=?", c.bindings, c.task); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE runtime_instances SET state='FAILED',details='[]' WHERE challenge_id IN (?,?)", c.task, c.question); err != nil {
			return err
		}
		sets[c.set] = true
	}
	for id := range sets {
		if _, err := tx.ExecContext(ctx, "UPDATE challenge_sets SET version=version+1,updated_at=? WHERE id=?", time.Now().UnixMilli(), id); err != nil {
			return err
		}
	}
	return nil
}
