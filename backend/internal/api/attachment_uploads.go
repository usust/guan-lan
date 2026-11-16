package api

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"huashan/internal/auth"
	"huashan/internal/storage"
)

type attachmentUpload struct {
	State     string          `json:"state"`
	Error     string          `json:"error,omitempty"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Size      int64           `json:"size"`
	Offset    int64           `json:"offset"`
	Chunks    []uploadChunk   `json:"chunks"`
	ChunkSize int64           `json:"chunk_size"`
	Result    json.RawMessage `json:"result,omitempty"`
}

func (s *Server) attachmentLock(id string) *sync.Mutex {
	value, _ := s.uploadLocks.LoadOrStore(id, &sync.Mutex{})
	return value.(*sync.Mutex)
}
func (s *Server) attachmentFile(id string) string {
	// IDs are obtained from the database, never from a client-supplied path.
	return filepath.Join(s.root.Path, "temp", "attachment-"+id)
}
func (s *Server) findAttachmentUpload(c *gin.Context, id string, withChunks bool) (*attachmentUpload, bool) {
	u := &attachmentUpload{ChunkSize: packageChunkBytes, Chunks: []uploadChunk{}}
	var result string
	tx, err := s.auth.DB.BeginTx(c.Request.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		handleError(c, err)
		return nil, false
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(c.Request.Context(),
		"SELECT id,name,size,offset,result_json,state,error FROM attachment_uploads WHERE id=? AND actor_id=?",
		id, actor(c)).Scan(&u.ID, &u.Name, &u.Size, &u.Offset, &result, &u.State, &u.Error)
	if errors.Is(err, sql.ErrNoRows) {
		fail(c, 404, "UPLOAD_NOT_FOUND", "上传记录不存在，请重新选择文件")
		return nil, false
	}
	if err != nil {
		handleError(c, err)
		return nil, false
	}
	if withChunks {
		rows, err := tx.QueryContext(c.Request.Context(), "SELECT size,sha256 FROM attachment_upload_chunks WHERE upload_id=? ORDER BY offset", u.ID)
		if err != nil {
			handleError(c, err)
			return nil, false
		}
		for rows.Next() {
			var chunk uploadChunk
			if err = rows.Scan(&chunk.Size, &chunk.SHA256); err != nil {
				rows.Close()
				handleError(c, err)
				return nil, false
			}
			u.Chunks = append(u.Chunks, chunk)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			handleError(c, err)
			return nil, false
		}
	}
	if result != "" {
		u.Result = json.RawMessage(result)
	}
	if err = tx.Commit(); err != nil {
		handleError(c, err)
		return nil, false
	}
	return u, true
}
func (s *Server) registerAttachmentUploads(admin *gin.RouterGroup) {
	admin.POST("/attachment-uploads", s.createAttachmentUpload)
	admin.GET("/attachment-uploads/:upload", func(c *gin.Context) {
		if u, found := s.findAttachmentUpload(c, c.Param("upload"), true); found {
			ok(c, 200, u)
		}
	})
	admin.PUT("/attachment-uploads/:upload", s.appendAttachmentChunk)
	admin.POST("/attachment-uploads/:upload/complete", s.completeAttachmentUpload)
}

func (s *Server) createAttachmentUpload(c *gin.Context) {
	var input struct {
		Name        string `json:"name"`
		Size        int64  `json:"size"`
		Fingerprint string `json:"fingerprint"`
	}
	if c.ShouldBindJSON(&input) != nil || input.Size <= 0 || strings.TrimSpace(input.Name) == "" ||
		len(input.Name) > 1024 || len(input.Fingerprint) != 64 {
		fail(c, 400, "INVALID_INPUT", "请选择非空附件")
		return
	}
	if _, err := hex.DecodeString(input.Fingerprint); err != nil {
		fail(c, 400, "INVALID_INPUT", "文件指纹无效")
		return
	}
	// Serialize create/resume for the same owner and file. Sessions survive browser and server restarts.
	lock := s.attachmentLock(actor(c) + ":" + input.Fingerprint)
	lock.Lock()
	defer lock.Unlock()
	var id string
	err := s.auth.DB.QueryRowContext(c.Request.Context(),
		"SELECT id FROM attachment_uploads WHERE actor_id=? AND fingerprint=? AND size=? ORDER BY updated_at DESC LIMIT 1",
		actor(c), input.Fingerprint, input.Size).Scan(&id)
	if err == nil {
		u, found := s.findAttachmentUpload(c, id, true)
		if found {
			ok(c, 200, u)
		}
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		handleError(c, err)
		return
	}
	// Capacity is rechecked for every chunk; no file-size ceiling or preallocation.
	if err = storage.CheckDiskBudget(s.root.Path, 0, s.config.MinFreeBytes); err != nil {
		fail(c, 507, "INSUFFICIENT_STORAGE", "磁盘剩余空间不足")
		return
	}
	id = auth.RandomToken()
	f, err := os.OpenFile(s.attachmentFile(id), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		handleError(c, err)
		return
	}
	if err = f.Close(); err != nil {
		_ = os.Remove(s.attachmentFile(id))
		handleError(c, err)
		return
	}
	_, err = s.auth.DB.ExecContext(c.Request.Context(),
		"INSERT INTO attachment_uploads(id,actor_id,fingerprint,name,size,updated_at) VALUES(?,?,?,?,?,?)",
		id, actor(c), input.Fingerprint, input.Name, input.Size, time.Now().UnixMilli())
	if err != nil {
		_ = os.Remove(s.attachmentFile(id))
		handleError(c, err)
		return
	}
	ok(c, 200, &attachmentUpload{ID: id, Name: input.Name, Size: input.Size, State: "uploading", Chunks: []uploadChunk{}, ChunkSize: packageChunkBytes})
}

func (s *Server) appendAttachmentChunk(c *gin.Context) {
	lock := s.attachmentLock(c.Param("upload"))
	lock.Lock()
	defer lock.Unlock()
	u, found := s.findAttachmentUpload(c, c.Param("upload"), false)
	if !found {
		return
	}
	offset, err := strconv.ParseInt(c.GetHeader("Upload-Offset"), 10, 64)
	if err != nil || offset != u.Offset || len(u.Result) != 0 {
		fail(c, 409, "UPLOAD_OFFSET_MISMATCH", "上传位置已变化，请继续上传以同步进度")
		return
	}
	size := c.Request.ContentLength
	if size <= 0 || size > packageChunkBytes || size > u.Size-u.Offset {
		fail(c, 400, "INVALID_CHUNK", "分片大小无效")
		return
	}
	expected := c.GetHeader("X-Chunk-SHA256")
	if len(expected) != 64 {
		fail(c, 400, "INVALID_CHUNK", "缺少分片摘要")
		return
	}
	if err = storage.CheckDiskBudget(s.root.Path, uint64(size), s.config.MinFreeBytes); err != nil {
		fail(c, 507, "INSUFFICIENT_STORAGE", "磁盘剩余空间不足，上传进度已保留")
		return
	}
	f, err := os.OpenFile(s.attachmentFile(u.ID), os.O_WRONLY, 0600)
	if err != nil {
		handleError(c, err)
		return
	}
	defer f.Close()
	// The committed DB offset is authoritative after a crash or interrupted write.
	if err = f.Truncate(u.Offset); err != nil {
		handleError(c, err)
		return
	}
	if _, err = f.Seek(u.Offset, io.SeekStart); err != nil {
		handleError(c, err)
		return
	}
	committed := false
	defer func() {
		if !committed {
			_ = f.Truncate(u.Offset)
		}
	}()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(c.Request.Body, size+1))
	if err != nil || n != size || fmt.Sprintf("%x", hash.Sum(nil)) != expected {
		fail(c, 400, "INVALID_CHUNK", "分片不完整或摘要不一致，请重试")
		return
	}
	if err = f.Sync(); err != nil {
		handleError(c, err)
		return
	}
	tx, err := s.auth.DB.BeginTx(c.Request.Context(), nil)
	if err != nil {
		handleError(c, err)
		return
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(c.Request.Context(), "INSERT INTO attachment_upload_chunks(upload_id,offset,size,sha256) VALUES(?,?,?,?)", u.ID, offset, n, expected)
	if err == nil {
		_, err = tx.ExecContext(c.Request.Context(),
			"UPDATE attachment_uploads SET offset=?,updated_at=? WHERE id=? AND actor_id=?",
			offset+n, time.Now().UnixMilli(), u.ID, actor(c))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		handleError(c, err)
		return
	}
	committed = true
	u.Offset += n
	ok(c, 200, u)
}

func (s *Server) completeAttachmentUpload(c *gin.Context) {
	lock := s.attachmentLock(c.Param("upload"))
	lock.Lock()
	defer lock.Unlock()
	u, found := s.findAttachmentUpload(c, c.Param("upload"), false)
	if !found {
		return
	}
	if len(u.Result) > 0 {
		ok(c, 200, u)
		return
	}
	if u.Offset != u.Size {
		fail(c, 409, "UPLOAD_INCOMPLETE", "附件尚未上传完成")
		return
	}
	info, err := os.Stat(s.attachmentFile(u.ID))
	if err != nil || info.Size() != u.Size {
		fail(c, 409, "UPLOAD_INCOMPLETE", "附件文件不完整，请重新上传")
		return
	}
	// Keep the source until the database commit, making finalization retryable.
	target := filepath.Join(s.root.Path, "uploads/attachments", u.ID)
	if err = os.Link(s.attachmentFile(u.ID), target); err != nil && !os.IsExist(err) {
		handleError(c, err)
		return
	}
	result, err := json.Marshal(gin.H{"name": u.Name, "data": "", "file_id": u.ID, "size": u.Size})
	if err != nil {
		handleError(c, err)
		return
	}
	_, err = s.auth.DB.ExecContext(c.Request.Context(), "UPDATE attachment_uploads SET state='done',result_json=?,updated_at=? WHERE id=?", string(result), time.Now().UnixMilli(), u.ID)
	if err != nil {
		handleError(c, err)
		return
	}
	_ = os.Remove(s.attachmentFile(u.ID))
	u.State = "done"
	u.Result = result
	ok(c, 200, u)
}

// Downloads are streamed by net/http (including Range support), never Base64 encoded.
func (s *Server) downloadStoredAttachment(c *gin.Context) {
	id := c.Param("file")
	var name string
	err := s.auth.DB.QueryRowContext(c.Request.Context(), "SELECT name FROM attachment_uploads WHERE id=? AND state='done'", id).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		fail(c, 404, "NOT_FOUND", "附件不存在")
		return
	}
	if err != nil {
		handleError(c, err)
		return
	}
	allowed := isAdmin(c)
	if !allowed {
		// Check exact file references without loading entire sets (including inline
		// attachments) or running the write-side binding synchronization in Get.
		err = s.auth.DB.QueryRowContext(c.Request.Context(), `SELECT EXISTS (
          SELECT 1 FROM challenges c JOIN challenge_sets s ON s.id=c.set_id
          WHERE s.status='PUBLISHED'
            AND NOT EXISTS (SELECT 1 FROM question_library l WHERE l.set_id=s.id)
            AND (EXISTS (SELECT 1 FROM json_each(c.attachments_json) a
                         WHERE json_extract(a.value,'$.file_id')=?)
              OR EXISTS (SELECT 1 FROM subtasks t JOIN subtask_resources r ON r.subtask_id=t.id,
                         json_each(r.attachments_json) a
                         WHERE t.challenge_id=c.id AND json_extract(a.value,'$.file_id')=?))
        )`, id, id).Scan(&allowed)
		if err != nil {
			handleError(c, err)
			return
		}
	}
	if !allowed {
		fail(c, 404, "NOT_FOUND", "附件不存在或不可访问")
		return
	}
	file, err := os.Open(filepath.Join(s.root.Path, "uploads/attachments", id))
	if err != nil {
		if os.IsNotExist(err) {
			fail(c, 404, "NOT_FOUND", "附件文件不存在，请联系管理员重新上传")
		} else {
			handleError(c, err)
		}
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		fail(c, 404, "NOT_FOUND", "附件文件不存在")
		return
	}
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	http.ServeContent(c.Writer, c.Request, name, info.ModTime(), file)
}
