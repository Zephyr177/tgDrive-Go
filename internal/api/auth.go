package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Zephyr177/tgDrive-Go/internal/auth"
	"github.com/Zephyr177/tgDrive-Go/internal/model"
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type registerRequest struct {
	Username        string `json:"username"`
	Password        string `json:"password"`
	ConfirmPassword string `json:"confirmPassword"`
	Email           string `json:"email"`
}

type changePasswordRequest struct {
	OldPassword string `json:"oldPassword"`
	NewPassword string `json:"newPassword"`
}

type adminChangePasswordRequest struct {
	Username    string `json:"username"`
	NewPassword string `json:"newPassword"`
}

// userLogin matches the upstream wire format (capitalized UserId).
type userLogin struct {
	Token    string `json:"token"`
	UserID   int64  `json:"UserId"`
	Role     string `json:"role"`
	Username string `json:"username"`
	Email    string `json:"email"`
	ExpireAt *int64 `json:"expireAt"`
}

// issueLogin creates a session token, updates last_login_time and sets the
// sa-token compatible cookie.
func (s *Server) issueLogin(w http.ResponseWriter, user *model.User) (*userLogin, error) {
	token, err := s.Auth.Create(user.ID)
	if err != nil {
		return nil, err
	}
	_ = s.Store.UpdateLastLoginTime(user.ID, nowString())
	expires := time.Now().Add(s.Auth.TTL()).UnixMilli()
	http.SetCookie(w, &http.Cookie{
		Name:     tokenName,
		Value:    token,
		Path:     "/",
		Expires:  time.Now().Add(s.Auth.TTL()),
		HttpOnly: true,
	})
	return &userLogin{
		Token: token, UserID: user.ID, Role: user.Role,
		Username: user.Username, Email: user.Email, ExpireAt: &expires,
	}, nil
}

// handleLogin implements POST /api/auth/login (username or email).
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeValidationError(w, "请求体格式错误")
		return
	}
	if strings.TrimSpace(req.Username) == "" {
		writeValidationError(w, "用户名不能为空")
		return
	}
	if strings.TrimSpace(req.Password) == "" {
		writeValidationError(w, "密码不能为空")
		return
	}
	if len(req.Password) < 5 {
		writeValidationError(w, "密码最短为5位")
		return
	}

	user, err := s.Store.UserByUsernameOrEmail(req.Username)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, "用户不存在")
		return
	}
	if err != nil {
		writeError(w, "登录失败")
		return
	}
	if !auth.CheckPassword(user.Password, req.Password) {
		writeError(w, "密码错误")
		return
	}
	res, err := s.issueLogin(w, user)
	if err != nil {
		writeError(w, "登录失败")
		return
	}
	writeResult(w, res)
}

// handleRegister implements POST /api/auth/register (gated by allow_registration).
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := decodeJSON(r, &req); err != nil {
		writeValidationError(w, "请求体格式错误")
		return
	}
	if strings.TrimSpace(req.Username) == "" {
		writeValidationError(w, "用户名不能为空")
		return
	}
	if strings.TrimSpace(req.Password) == "" {
		writeValidationError(w, "密码不能为空")
		return
	}
	if len(req.Password) < 5 {
		writeValidationError(w, "密码至少5位")
		return
	}

	allowed := false
	if v, err := s.Store.GetSetting("allow_registration"); err == nil {
		allowed = v == "true"
	}
	if !allowed {
		writeError(w, "注册功能已关闭")
		return
	}

	if _, err := s.Store.UserByUsername(req.Username); err == nil {
		writeError(w, "用户名已存在")
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		writeError(w, "注册失败")
		return
	}
	if req.Email != "" {
		if _, err := s.Store.UserByEmail(req.Email); err == nil {
			writeError(w, "邮箱已存在")
			return
		} else if !errors.Is(err, sql.ErrNoRows) {
			writeError(w, "注册失败")
			return
		}
	}
	if len(req.Password) < 6 {
		writeError(w, "密码长度至少为6位")
		return
	}
	if req.Password != req.ConfirmPassword {
		writeError(w, "两次输入的密码不一致")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, "注册失败")
		return
	}
	id, err := s.Store.CreateUser(req.Username, hash, "user", req.Email)
	if err != nil {
		writeError(w, "用户名已存在")
		return
	}
	user := &model.User{ID: id, Username: req.Username, Role: "user", Email: req.Email}
	res, err := s.issueLogin(w, user)
	if err != nil {
		writeError(w, "注册失败")
		return
	}
	writeResult(w, res)
}

// handleChangePassword implements POST /api/auth/change-password.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	u := s.requireLogin(w, r)
	if u == nil {
		return
	}
	var req changePasswordRequest
	if err := decodeJSON(r, &req); err != nil {
		writeValidationError(w, "请求体格式错误")
		return
	}
	if strings.TrimSpace(req.OldPassword) == "" {
		writeValidationError(w, "旧密码不能为空")
		return
	}
	if strings.TrimSpace(req.NewPassword) == "" {
		writeValidationError(w, "新密码不能为空")
		return
	}
	if len(req.NewPassword) < 5 {
		writeValidationError(w, "密码至少为5位")
		return
	}
	if !auth.CheckPassword(u.Password, req.OldPassword) {
		writeError(w, "原密码错误")
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeError(w, "密码修改失败")
		return
	}
	if err := s.Store.UpdateUserPassword(u.ID, hash); err != nil {
		writeError(w, "密码修改失败")
		return
	}
	writeResult(w, "密码修改成功")
}

// handleLogout revokes the current token (extension; upstream has no logout).
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if token := s.tokenFromRequest(r); token != "" {
		s.Auth.Revoke(token)
	}
	http.SetCookie(w, &http.Cookie{Name: tokenName, Value: "", Path: "/", MaxAge: -1})
	writeResult(w, "退出成功")
}

// handleAdminChangePassword implements POST /api/auth/admin/change-password.
func (s *Server) handleAdminChangePassword(w http.ResponseWriter, r *http.Request) {
	if s.requireAdmin(w, r) == nil {
		return
	}
	var req adminChangePasswordRequest
	if err := decodeJSON(r, &req); err != nil {
		writeValidationError(w, "请求体格式错误")
		return
	}
	if strings.TrimSpace(req.Username) == "" {
		writeValidationError(w, "用户名不能为空")
		return
	}
	if strings.TrimSpace(req.NewPassword) == "" {
		writeValidationError(w, "新密码不能为空")
		return
	}
	if len(req.NewPassword) < 5 {
		writeValidationError(w, "新密码长度不能少于5位")
		return
	}
	user, err := s.Store.UserByUsername(req.Username)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, "用户不存在")
		return
	}
	if err != nil {
		writeError(w, "密码修改失败")
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeError(w, "密码修改失败")
		return
	}
	if err := s.Store.UpdateUserPassword(user.ID, hash); err != nil {
		writeError(w, "密码修改失败")
		return
	}
	writeResult(w, "密码修改成功")
}

// handleListUsers implements GET /api/auth/admin/users (passwords never serialized).
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if s.requireAdmin(w, r) == nil {
		return
	}
	users, err := s.Store.ListUsers()
	if err != nil {
		writeError(w, "获取用户列表失败")
		return
	}
	if users == nil {
		users = []model.User{}
	}
	writeResult(w, users)
}

// handleDeleteUser implements DELETE /api/auth/admin/users/{userId}.
func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if s.requireAdmin(w, r) == nil {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("userId"), 10, 64)
	if err != nil {
		writeValidationError(w, "userID不能为空")
		return
	}
	target, err := s.Store.UserByID(id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, "用户不存在")
		return
	}
	if err != nil {
		writeError(w, "删除失败")
		return
	}
	if target.Role == "admin" {
		writeError(w, "不能删除管理员账户")
		return
	}
	if err := s.Store.DeleteUser(id); err != nil {
		writeError(w, "删除失败")
		return
	}
	writeResult(w, "用户删除成功")
}

// handleUpdateUserRole implements PUT /api/auth/admin/users/{userId}/role
// (called by the bundled frontend although upstream never implemented it).
func (s *Server) handleUpdateUserRole(w http.ResponseWriter, r *http.Request) {
	if s.requireAdmin(w, r) == nil {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("userId"), 10, 64)
	if err != nil {
		writeValidationError(w, "userID不能为空")
		return
	}
	role := r.URL.Query().Get("role")
	switch role {
	case "admin", "user", "visitor":
	default:
		writeValidationError(w, "角色只能是 admin、user 或 visitor")
		return
	}
	target, err := s.Store.UserByID(id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, "用户不存在")
		return
	}
	if err != nil {
		writeError(w, "更新失败")
		return
	}
	if err := s.Store.UpdateUserRole(target.ID, role); err != nil {
		writeError(w, "更新失败")
		return
	}
	writeResult(w, "更新成功")
}
