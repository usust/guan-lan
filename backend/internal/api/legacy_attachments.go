package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"huashan/internal/competition"
)

// Convert legacy inline files before returning a set. Persist the references so
// subsequent readers never receive or decode the old Base64 payloads.
func (s *Server) migrateLegacyAttachments(ctx context.Context, set, owner string) error {
	lock := s.attachmentLock("legacy-attachments")
	lock.Lock()
	defer lock.Unlock()
	for _, spec := range []struct{ table, key, scope string }{
		{"challenges", "id", "set_id=?"},
		{"subtask_resources", "subtask_id", "subtask_id IN (SELECT t.id FROM subtasks t JOIN challenges c ON c.id=t.challenge_id WHERE c.set_id=?)"},
	} {
		for {
			var id, raw string
			err := s.auth.DB.QueryRowContext(ctx, "SELECT "+spec.key+",attachments_json FROM "+spec.table+" WHERE "+spec.scope+" AND EXISTS (SELECT 1 FROM json_each(attachments_json) a WHERE length(COALESCE(json_extract(a.value,'$.data'),''))>0) LIMIT 1", set).Scan(&id, &raw)
			if errors.Is(err, sql.ErrNoRows) {
				break
			}
			if err != nil {
				return err
			}
			var files []competition.Attachment
			if err = json.Unmarshal([]byte(raw), &files); err != nil {
				return err
			}
			for i := range files {
				if files[i].Data == "" {
					continue
				}
				if err = s.storeLegacyAttachment(ctx, &files[i], owner); err != nil {
					return err
				}
			}
			data, err := json.Marshal(files)
			if err != nil {
				return err
			}
			// Compare-and-swap protects an administrator's concurrent edits.
			if _, err = s.auth.DB.ExecContext(ctx, "UPDATE "+spec.table+" SET attachments_json=? WHERE "+spec.key+"=? AND attachments_json=?", string(data), id, raw); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Server) storeLegacyAttachment(ctx context.Context, a *competition.Attachment, owner string) error {
	f, err := os.CreateTemp(filepath.Join(s.root.Path, "uploads/attachments"), ".legacy-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	hash := sha256.New()
	_, _ = io.WriteString(hash, a.Name+"\x00")
	size, err := io.Copy(io.MultiWriter(f, hash), base64.NewDecoder(base64.StdEncoding, strings.NewReader(a.Data)))
	if err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	id := hex.EncodeToString(hash.Sum(nil))
	target := filepath.Join(s.root.Path, "uploads/attachments", id)
	if err = os.Rename(f.Name(), target); err != nil {
		return err
	}
	_, err = s.auth.DB.ExecContext(ctx, `INSERT INTO attachment_uploads(id,actor_id,fingerprint,name,size,offset,state,updated_at) VALUES(?,?,?,?,?,?,'done',?) ON CONFLICT(id) DO NOTHING`, id, owner, id, a.Name, size, size, time.Now().UnixMilli())
	if err != nil {
		return err
	}
	a.Data, a.FileID, a.Size = "", id, size
	return nil
}
