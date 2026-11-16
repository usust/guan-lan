package api

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"huashan/internal/auth"
	"huashan/internal/environment"
	"huashan/internal/storage"
)

// This is a per-request bound, not a package size limit.
const packageChunkBytes int64 = 8 << 20

type uploadChunk struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type packageUpload struct {
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

func (s *Server) uploadLock(id string) *sync.Mutex {
	value, _ := s.uploadLocks.LoadOrStore(id, &sync.Mutex{})
	return value.(*sync.Mutex)
}
func (s *Server) uploadFile(id string) string {
	// IDs are obtained from the database, never from a client-supplied path.
	return filepath.Join(s.root.Path, "temp", "resume-"+id)
}
func (s *Server) findUpload(c *gin.Context, id string, withChunks bool) (*packageUpload, bool) {
	u := &packageUpload{ChunkSize: packageChunkBytes, Chunks: []uploadChunk{}}
	var result string
	tx, err := s.auth.DB.BeginTx(c.Request.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		handleError(c, err)
		return nil, false
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(c.Request.Context(),
		"SELECT id,name,size,offset,result_json,state,error FROM package_uploads WHERE id=? AND actor_id=?",
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
		rows, err := tx.QueryContext(c.Request.Context(), "SELECT size,sha256 FROM package_upload_chunks WHERE upload_id=? ORDER BY offset", u.ID)
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
		var staged struct {
			Token string `json:"package_token"`
		}
		if err = json.Unmarshal([]byte(result), &staged); err != nil {
			handleError(c, err)
			return nil, false
		}
		var count int
		if err = tx.QueryRowContext(c.Request.Context(), "SELECT count(*) FROM staged_packages WHERE token=? AND actor_id=? AND expires_at>?", staged.Token, actor(c), time.Now().UnixMilli()).Scan(&count); err != nil {
			handleError(c, err)
			return nil, false
		}
		if count == 0 {
			fail(c, 410, "UPLOAD_EXPIRED", "暂存包已保存或过期，请重新上传")
			return nil, false
		}
		u.Result = json.RawMessage(result)
	}
	if err = tx.Commit(); err != nil {
		handleError(c, err)
		return nil, false
	}
	return u, true
}
func (s *Server) registerPackageUploads(admin *gin.RouterGroup) {
	admin.POST("/package-uploads", s.createPackageUpload)
	admin.GET("/package-uploads/:upload", func(c *gin.Context) {
		if u, found := s.findUpload(c, c.Param("upload"), true); found {
			ok(c, 200, u)
		}
	})
	admin.PUT("/package-uploads/:upload", s.appendPackageChunk)
	admin.POST("/package-uploads/:upload/complete", s.completePackageUpload)
}

func (s *Server) createPackageUpload(c *gin.Context) {
	var input struct {
		Name        string `json:"name"`
		Size        int64  `json:"size"`
		Fingerprint string `json:"fingerprint"`
	}
	if c.ShouldBindJSON(&input) != nil || input.Size <= 0 ||
		len(input.Name) > 1024 || !validPackageFilename(input.Name) || len(input.Fingerprint) != 64 {
		fail(c, 400, "INVALID_INPUT", "请选择有效的 TAR 或 TAR.GZ 包")
		return
	}
	if _, err := hex.DecodeString(input.Fingerprint); err != nil {
		fail(c, 400, "INVALID_INPUT", "文件指纹无效")
		return
	}
	// Serialize create/resume for the same owner and file. Sessions survive browser and server restarts.
	lock := s.uploadLock(actor(c) + ":" + input.Fingerprint)
	lock.Lock()
	defer lock.Unlock()
	var id string
	err := s.auth.DB.QueryRowContext(c.Request.Context(),
		"SELECT u.id FROM package_uploads u WHERE u.actor_id=? AND u.fingerprint=? AND u.size=? AND (u.state IN ('uploading','validating') OR (u.state='done' AND EXISTS(SELECT 1 FROM staged_packages p WHERE p.package_path=u.id||'.tar' AND p.actor_id=u.actor_id AND p.expires_at>?))) ORDER BY u.updated_at DESC LIMIT 1",
		actor(c), input.Fingerprint, input.Size, time.Now().UnixMilli()).Scan(&id)
	if err == nil {
		u, found := s.findUpload(c, id, true)
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
	f, err := os.OpenFile(s.uploadFile(id), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		handleError(c, err)
		return
	}
	if err = f.Close(); err != nil {
		_ = os.Remove(s.uploadFile(id))
		handleError(c, err)
		return
	}
	_, err = s.auth.DB.ExecContext(c.Request.Context(),
		"INSERT INTO package_uploads(id,actor_id,fingerprint,name,size,updated_at) VALUES(?,?,?,?,?,?)",
		id, actor(c), input.Fingerprint, input.Name, input.Size, time.Now().UnixMilli())
	if err != nil {
		_ = os.Remove(s.uploadFile(id))
		handleError(c, err)
		return
	}
	ok(c, 200, &packageUpload{ID: id, Name: input.Name, Size: input.Size, State: "uploading", Chunks: []uploadChunk{}, ChunkSize: packageChunkBytes})
}

func (s *Server) appendPackageChunk(c *gin.Context) {
	lock := s.uploadLock(c.Param("upload"))
	lock.Lock()
	defer lock.Unlock()
	u, found := s.findUpload(c, c.Param("upload"), false)
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
	f, err := os.OpenFile(s.uploadFile(u.ID), os.O_WRONLY, 0600)
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
	_, err = tx.ExecContext(c.Request.Context(), "INSERT INTO package_upload_chunks(upload_id,offset,size,sha256) VALUES(?,?,?,?)", u.ID, offset, n, expected)
	if err == nil {
		_, err = tx.ExecContext(c.Request.Context(),
			"UPDATE package_uploads SET offset=?,updated_at=? WHERE id=? AND actor_id=?",
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

func (s *Server) completePackageUpload(c *gin.Context) {
	lock := s.uploadLock(c.Param("upload"))
	if !lock.TryLock() {
		// Authenticate ownership even when another request is processing this ID.
		if _, found := s.findUpload(c, c.Param("upload"), false); found {
			ok(c, 202, gin.H{"state": "validating"})
		}
		return
	}
	u, found := s.findUpload(c, c.Param("upload"), false)
	if !found {
		lock.Unlock()
		return
	}
	if len(u.Result) > 0 {
		lock.Unlock()
		ok(c, 200, u)
		return
	}
	if u.State == "error" && c.Query("retry") != "1" {
		lock.Unlock()
		fail(c, 400, "INVALID_PACKAGE", u.Error)
		return
	}
	if u.Offset != u.Size {
		lock.Unlock()
		fail(c, 409, "UPLOAD_INCOMPLETE", "文件尚未上传完成")
		return
	}
	select {
	case s.packageSlots <- struct{}{}:
	default:
		lock.Unlock()
		fail(c, 429, "PACKAGE_UPLOAD_BUSY", "已有环境包正在校验，请稍后继续")
		return
	}
	_, err := s.auth.DB.ExecContext(c.Request.Context(), "UPDATE package_uploads SET state='validating',error='' WHERE id=?", u.ID)
	if err != nil {
		<-s.packageSlots
		lock.Unlock()
		handleError(c, err)
		return
	}
	owner := actor(c)
	// Validation is independent of HTTP timeouts and a browser disconnect.
	go func() {
		defer lock.Unlock()
		defer func() { <-s.packageSlots }()
		if err := s.validatePackageUpload(u, owner); err != nil {
			_, _ = s.auth.DB.Exec("UPDATE package_uploads SET state='error',error=?,updated_at=? WHERE id=?", err.Error(), time.Now().UnixMilli(), u.ID)
		}
	}()
	u.State = "validating"
	ok(c, 202, u)
}

func validPackageFilename(name string) bool {
	name = strings.ToLower(name)
	return strings.HasSuffix(name, ".tar") || strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".tgz")
}

// Expand to a temporary TAR without changing the resumable upload. Check disk
// space before each bounded write, since compressed size cannot predict TAR size.
func (s *Server) expandPackageGzip(src *os.File) (*os.File, error) {
	reader, err := gzip.NewReader(src)
	if err != nil {
		return nil, fmt.Errorf("GZIP 格式无效：%w", err)
	}
	defer reader.Close()
	dst, err := os.CreateTemp(filepath.Join(s.root.Path, "temp"), "expanded-package-*")
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			dst.Close()
			os.Remove(dst.Name())
		}
	}()
	buffer := make([]byte, 1<<20)
	for {
		n, readErr := reader.Read(buffer)
		if n > 0 {
			if err = storage.CheckDiskBudget(s.root.Path, uint64(n), s.config.MinFreeBytes); err != nil {
				return nil, fmt.Errorf("解压环境包所需磁盘空间不足：%w", err)
			}
			if _, err = dst.Write(buffer[:n]); err != nil {
				return nil, err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("GZIP 解压或完整性校验失败：%w", readErr)
		}
	}
	if _, err = dst.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	success = true
	return dst, nil
}

func (s *Server) validatePackageUpload(u *packageUpload, owner string) error {
	f, err := os.Open(s.uploadFile(u.ID))
	if err != nil {
		return err
	}
	stat, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	if stat.Size() != u.Size {
		f.Close()
		return fmt.Errorf("上传文件长度不一致")
	}
	defer f.Close()
	source := s.uploadFile(u.ID)
	// Detect compression by content; file extensions are only a UI convenience.
	var magic [2]byte
	n, _ := f.ReadAt(magic[:], 0)
	if n == 2 && magic == [2]byte{0x1f, 0x8b} {
		expanded, expandErr := s.expandPackageGzip(f)
		if expandErr != nil {
			return expandErr
		}
		defer expanded.Close()
		defer os.Remove(expanded.Name())
		f = expanded
		source = expanded.Name()
	}
	stat, err = f.Stat()
	if err != nil {
		return err
	}
	hash := sha256.New()
	manifest, err := environment.Inspect(io.TeeReader(f, hash))
	if err != nil {
		// A Docker-save TAR has no platform manifest. Wrap it without changing
		// the resumable source, so interruption and retries remain safe.
		if err = storage.CheckDiskBudget(s.root.Path, uint64(stat.Size())+(1<<20), s.config.MinFreeBytes); err != nil {
			return fmt.Errorf("转换 Docker 镜像所需磁盘空间不足: %w", err)
		}
		converted, createErr := os.CreateTemp(filepath.Join(s.root.Path, "temp"), "docker-package-*")
		if createErr != nil {
			return createErr
		}
		defer os.Remove(converted.Name())
		err = environment.WrapDockerArchive(f, converted)
		closeErr := converted.Close()
		if err != nil {
			return fmt.Errorf("环境包校验失败：%w", err)
		}
		if closeErr != nil {
			return closeErr
		}
		normalized, openErr := os.Open(converted.Name())
		if openErr != nil {
			return openErr
		}
		hash.Reset()
		manifest, err = environment.Inspect(io.TeeReader(normalized, hash))
		normalized.Close()
		if err != nil {
			return fmt.Errorf("转换后的环境包校验失败：%w", err)
		}
		source = converted.Name()
	}
	for i := range manifest.Images {
		manifest.Images[i].SourceFile = u.Name
	}
	token := auth.RandomToken()
	name := u.ID + ".tar"
	target := filepath.Join(s.root.Path, "uploads/packages", name)
	// Retain the source link until commit, so an interrupted finalization is retryable.
	if err = os.Link(source, target); os.IsExist(err) {
		// Replace a file left by an interrupted transaction with this attempt.
		if err = os.Remove(target); err == nil {
			err = os.Link(source, target)
		}
	}
	if err != nil {
		return err
	}
	digest := fmt.Sprintf("%x", hash.Sum(nil))
	manifest.AssignServiceIDs(digest)
	env, _ := json.Marshal(manifest)
	result, _ := json.Marshal(gin.H{"package_token": token, "environment": manifest, "package_sha256": digest})
	tx, err := s.auth.DB.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(
		"INSERT INTO staged_packages(token,actor_id,environment_json,package_path,package_sha256,expires_at) VALUES(?,?,?,?,?,?)",
		token, owner, string(env), name, digest, time.Now().Add(7*24*time.Hour).UnixMilli())
	if err == nil {
		_, err = tx.Exec("UPDATE package_uploads SET result_json=?,state='done',updated_at=? WHERE id=?", string(result), time.Now().UnixMilli(), u.ID)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		return err
	}
	_ = os.Remove(s.uploadFile(u.ID))
	return nil
}
