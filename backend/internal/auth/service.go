package auth

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"time"
)

var ErrCredentials = errors.New("invalid credentials")
var ErrConflict = errors.New("operation conflicts with existing state")
var ErrValidation = errors.New("invalid user fields")
var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{2,63}$`)

type User struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Status      string `json:"status"`
}
type Service struct {
	DB    *sql.DB
	dummy string
}

func New(db *sql.DB) *Service { h, _ := HashPassword(RandomToken()); return &Service{DB: db, dummy: h} }
func (s *Service) CreateUser(ctx context.Context, actor, username, display, password, role string, bootstrap bool) (User, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	display = strings.TrimSpace(display)
	if !usernamePattern.MatchString(username) || len(display) == 0 || len(display) > 128 || (role != "ADMIN" && role != "PLAYER") {
		return User{}, ErrValidation
	}
	hash, err := HashPassword(password)
	if err != nil {
		return User{}, ErrValidation
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	if bootstrap {
		var n int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM users").Scan(&n); err != nil {
			return User{}, err
		}
		if n != 0 {
			return User{}, ErrConflict
		}
	}
	var n int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM users WHERE username=?", username).Scan(&n); err != nil {
		return User{}, err
	}
	if n > 0 {
		return User{}, ErrConflict
	}
	u := User{RandomToken(), username, display, role, "ACTIVE"}
	_, err = tx.ExecContext(ctx, "INSERT INTO users(id,username,display_name,password_hash,role,status,created_at) VALUES(?,?,?,?,?,?,?)", u.ID, u.Username, u.DisplayName, hash, u.Role, u.Status, time.Now().UnixMilli())
	if err != nil {
		return User{}, err
	}
	var actorID any
	if actor != "" {
		actorID = actor
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO audit_logs(id,actor_id,action,target_id,created_at) VALUES(?,?,?,?,?)", RandomToken(), actorID, "user.create", u.ID, time.Now().UnixMilli())
	if err != nil {
		return User{}, err
	}
	return u, tx.Commit()
}
func (s *Service) Login(ctx context.Context, username, password string) (User, string, string, error) {
	var u User
	var hash string
	err := s.DB.QueryRowContext(ctx, "SELECT id,username,display_name,role,status,password_hash FROM users WHERE username=?", strings.ToLower(strings.TrimSpace(username))).Scan(&u.ID, &u.Username, &u.DisplayName, &u.Role, &u.Status, &hash)
	if err != nil {
		if err != sql.ErrNoRows {
			return u, "", "", err
		}
		_ = VerifyPassword(s.dummy, password)
		return u, "", "", ErrCredentials
	}
	if !VerifyPassword(hash, password) || u.Status != "ACTIVE" {
		return User{}, "", "", ErrCredentials
	}
	token, csrf := RandomToken(), RandomToken()
	// Conditional insert closes the disabled-user/login race.
	result, err := s.DB.ExecContext(ctx, "INSERT INTO sessions(token_hash,user_id,csrf_token,expires_at) SELECT ?,id,?,? FROM users WHERE id=? AND status='ACTIVE'", TokenHash(token), csrf, time.Now().Add(12*time.Hour).UnixMilli(), u.ID)
	if err != nil {
		return u, "", "", err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return User{}, "", "", ErrCredentials
	}
	return u, token, csrf, nil
}
func (s *Service) Session(ctx context.Context, token string) (User, string, error) {
	var u User
	var csrf string
	if len(token) != 64 {
		return u, csrf, ErrCredentials
	}
	err := s.DB.QueryRowContext(ctx, `SELECT u.id,u.username,u.display_name,u.role,u.status,s.csrf_token FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=? AND s.expires_at>? AND u.status='ACTIVE'`, TokenHash(token), time.Now().UnixMilli()).Scan(&u.ID, &u.Username, &u.DisplayName, &u.Role, &u.Status, &csrf)
	if err == sql.ErrNoRows {
		err = ErrCredentials
	}
	return u, csrf, err
}
func (s *Service) Logout(ctx context.Context, token string) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash=?", TokenHash(token))
	return err
}
func (s *Service) Users(ctx context.Context) ([]User, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id,username,display_name,role,status FROM users ORDER BY created_at,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []User{}
	for rows.Next() {
		var u User
		if err = rows.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Role, &u.Status); err != nil {
			return nil, err
		}
		items = append(items, u)
	}
	return items, rows.Err()
}
func (s *Service) SetStatus(ctx context.Context, actor, id, status string) error {
	if status != "ACTIVE" && status != "DISABLED" {
		return ErrValidation
	}
	if actor == id && status == "DISABLED" {
		return ErrConflict
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var role, current string
	if err = tx.QueryRowContext(ctx, "SELECT role,status FROM users WHERE id=?", id).Scan(&role, &current); err != nil {
		return err
	}
	if role == "ADMIN" && current == "ACTIVE" && status == "DISABLED" {
		var n int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM users WHERE role='ADMIN' AND status='ACTIVE'").Scan(&n); err != nil {
			return err
		}
		if n <= 1 {
			return ErrConflict
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE users SET status=? WHERE id=?", status, id); err != nil {
		return err
	}
	if status == "DISABLED" {
		if _, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id=?", id); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO audit_logs(id,actor_id,action,target_id,created_at) VALUES(?,?,?,?,?)", RandomToken(), actor, "user.status."+status, id, time.Now().UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}
