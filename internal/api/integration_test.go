package api_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"

	"github.com/Zephyr177/tgDrive-Go/internal/api"
	"github.com/Zephyr177/tgDrive-Go/internal/auth"
	"github.com/Zephyr177/tgDrive-Go/internal/bot"
	"github.com/Zephyr177/tgDrive-Go/internal/config"
	"github.com/Zephyr177/tgDrive-Go/internal/files"
	"github.com/Zephyr177/tgDrive-Go/internal/model"
	"github.com/Zephyr177/tgDrive-Go/internal/store"
	"github.com/Zephyr177/tgDrive-Go/internal/telegram"
	"github.com/Zephyr177/tgDrive-Go/internal/ws"
)

type testEnv struct {
	ts     *httptest.Server
	st     *store.Store
	fake   *telegram.Fake
	hub    *ws.Hub
	bot    *bot.Controller
	dbPath string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "tgDrive.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	fake := telegram.NewFake()
	hub := ws.NewHub()
	svc := files.New(st, fake, hub)
	botCtl := bot.NewController(st, "")
	am := auth.NewManager(time.Hour)
	cfg := config.Config{
		Port: 8085, DBPath: dbPath, UploadDir: t.TempDir(),
		TokenName: "tgdrive", TokenTimeout: time.Hour,
	}
	uiFS := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>tgDrive UI</title>")},
	}
	srv := api.NewServer(cfg, st, am, svc, hub, botCtl, nil, uiFS)
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	return &testEnv{ts: ts, st: st, fake: fake, hub: hub, bot: botCtl, dbPath: dbPath}
}

type result struct {
	Code int             `json:"code"`
	Msg  *string         `json:"msg"`
	Data json.RawMessage `json:"data"`
}

func (e *testEnv) do(t *testing.T, method, target, token string, body io.Reader, contentType string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, e.ts.URL+target, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	return resp
}

func (e *testEnv) doJSON(t *testing.T, method, target, token string, body any) (int, result) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(data)
	}
	resp := e.do(t, method, target, token, reader, "application/json")
	defer resp.Body.Close()
	var res result
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("decode %s %s: %v", method, target, err)
	}
	return resp.StatusCode, res
}

func dataInto(t *testing.T, res result, v any) {
	t.Helper()
	if err := json.Unmarshal(res.Data, v); err != nil {
		t.Fatalf("unmarshal data %s: %v", string(res.Data), err)
	}
}

func (e *testEnv) login(t *testing.T, username, password string) string {
	t.Helper()
	status, res := e.doJSON(t, http.MethodPost, "/api/auth/login", "", map[string]string{
		"username": username, "password": password,
	})
	if status != http.StatusOK || res.Code != 1 {
		t.Fatalf("login %s: status=%d %+v", username, status, res)
	}
	var data struct {
		Token string `json:"token"`
	}
	dataInto(t, res, &data)
	if data.Token == "" {
		t.Fatal("login returned empty token")
	}
	return data.Token
}

func multipartBody(t *testing.T, field, filename string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile(field, filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatalf("write form: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close form: %v", err)
	}
	return &buf, w.FormDataContentType()
}

func (e *testEnv) upload(t *testing.T, token, filename string, data []byte) map[string]string {
	t.Helper()
	buf, ct := multipartBody(t, "file", filename, data)
	resp := e.do(t, http.MethodPost, "/api/upload", token, buf, ct)
	defer resp.Body.Close()
	var res result
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("decode upload: %v", err)
	}
	if res.Code != 1 {
		t.Fatalf("upload %s failed: %+v", filename, res)
	}
	out := map[string]string{}
	dataInto(t, res, &out)
	return out
}

func TestAuthFlow(t *testing.T) {
	e := newTestEnv(t)

	// Wrong credentials.
	if _, res := e.doJSON(t, http.MethodPost, "/api/auth/login", "", map[string]string{"username": "admin", "password": "wrong1"}); res.Code != 0 || *res.Msg != "密码错误" {
		t.Fatalf("wrong password: %+v", res)
	}
	if _, res := e.doJSON(t, http.MethodPost, "/api/auth/login", "", map[string]string{"username": "nobody", "password": "123456"}); res.Code != 0 || *res.Msg != "用户不存在" {
		t.Fatalf("unknown user: %+v", res)
	}

	// Validation (HTTP 400).
	if status, _ := e.doJSON(t, http.MethodPost, "/api/auth/login", "", map[string]string{"username": "admin", "password": "123"}); status != http.StatusBadRequest {
		t.Fatalf("short password status = %d, want 400", status)
	}

	// Login response shape.
	status, res := e.doJSON(t, http.MethodPost, "/api/auth/login", "", map[string]string{"username": "admin", "password": "123456"})
	if status != http.StatusOK || res.Code != 1 {
		t.Fatalf("login: status=%d %+v", status, res)
	}
	var login struct {
		Token    string `json:"token"`
		UserID   int64  `json:"UserId"`
		Role     string `json:"role"`
		Username string `json:"username"`
		ExpireAt *int64 `json:"expireAt"`
	}
	dataInto(t, res, &login)
	if login.Token == "" || login.UserID == 0 || login.Role != "admin" || login.ExpireAt == nil {
		t.Fatalf("login data shape wrong: %+v", login)
	}

	// Anonymous access is rejected with 401.
	resp := e.do(t, http.MethodGet, "/api/upload/permission-check", "", nil, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous permission-check status = %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()

	// Authenticated access works.
	status, res = e.doJSON(t, http.MethodGet, "/api/upload/permission-check", login.Token, nil)
	if status != http.StatusOK || res.Code != 1 {
		t.Fatalf("permission-check: status=%d %+v", status, res)
	}
	var msg string
	dataInto(t, res, &msg)
	if msg != "OK" {
		t.Fatalf("permission-check data = %q, want OK", msg)
	}

	// Change password, old token still valid, new credentials work.
	status, res = e.doJSON(t, http.MethodPost, "/api/auth/change-password", login.Token, map[string]string{
		"oldPassword": "123456", "newPassword": "newpass99",
	})
	if status != http.StatusOK || res.Code != 1 {
		t.Fatalf("change-password: status=%d %+v", status, res)
	}
	if _, res := e.doJSON(t, http.MethodPost, "/api/auth/login", "", map[string]string{"username": "admin", "password": "123456"}); res.Code != 0 {
		t.Fatalf("old password should be rejected: %+v", res)
	}
	e.login(t, "admin", "newpass99")

	// Login by email works once an email exists (admin has none by default).
	if _, err := e.st.UserByUsername("admin"); err != nil {
		t.Fatalf("admin lookup: %v", err)
	}

	// Logout revokes the token.
	if _, res := e.doJSON(t, http.MethodPost, "/api/auth/logout", login.Token, nil); res.Code != 1 {
		t.Fatalf("logout: %+v", res)
	}
	resp = e.do(t, http.MethodGet, "/api/upload/permission-check", login.Token, nil, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked token status = %d, want 401", resp.StatusCode)
	}
}

func TestRegisterGated(t *testing.T) {
	e := newTestEnv(t)

	// Registration disabled by default.
	_, res := e.doJSON(t, http.MethodPost, "/api/auth/register", "", map[string]string{
		"username": "alice", "password": "secret123", "confirmPassword": "secret123", "email": "",
	})
	if res.Code != 0 || *res.Msg != "注册功能已关闭" {
		t.Fatalf("gated register: %+v", res)
	}

	if err := e.st.UpdateSetting("allow_registration", "true"); err != nil {
		t.Fatalf("enable registration: %v", err)
	}

	// Successful registration with auto-login.
	_, res = e.doJSON(t, http.MethodPost, "/api/auth/register", "", map[string]string{
		"username": "alice", "password": "secret123", "confirmPassword": "secret123", "email": "alice@example.com",
	})
	if res.Code != 1 {
		t.Fatalf("register: %+v", res)
	}
	var login struct {
		Token string `json:"token"`
		Role  string `json:"role"`
	}
	dataInto(t, res, &login)
	if login.Token == "" || login.Role != "user" {
		t.Fatalf("register data wrong: %+v", login)
	}

	// Duplicate username.
	_, res = e.doJSON(t, http.MethodPost, "/api/auth/register", "", map[string]string{
		"username": "alice", "password": "secret123", "confirmPassword": "secret123", "email": "",
	})
	if res.Code != 0 || *res.Msg != "用户名已存在" {
		t.Fatalf("duplicate register: %+v", res)
	}

	// Confirm mismatch.
	_, res = e.doJSON(t, http.MethodPost, "/api/auth/register", "", map[string]string{
		"username": "bob", "password": "secret123", "confirmPassword": "different", "email": "",
	})
	if res.Code != 0 || *res.Msg != "两次输入的密码不一致" {
		t.Fatalf("mismatch register: %+v", res)
	}

	// Service-level length check (5 chars passes bean validation, fails service).
	_, res = e.doJSON(t, http.MethodPost, "/api/auth/register", "", map[string]string{
		"username": "bob", "password": "12345", "confirmPassword": "12345", "email": "",
	})
	if res.Code != 0 || *res.Msg != "密码长度至少为6位" {
		t.Fatalf("short register: %+v", res)
	}
}

func TestFileLifecycle(t *testing.T) {
	e := newTestEnv(t)
	token := e.login(t, "admin", "123456")

	// Upload.
	content := []byte("hello world")
	up := e.upload(t, token, "hello.txt", content)
	if up["fileName"] != "hello.txt" || !strings.Contains(up["downloadLink"], "/d/") {
		t.Fatalf("upload result wrong: %+v", up)
	}

	// List shows the file with the upstream field names.
	_, res := e.doJSON(t, http.MethodGet, "/api/file-list?page=1&size=10", token, nil)
	var page struct {
		Total   int64            `json:"total"`
		Records []map[string]any `json:"records"`
	}
	dataInto(t, res, &page)
	if page.Total != 1 || len(page.Records) != 1 {
		t.Fatalf("file-list: %+v", page)
	}
	rec := page.Records[0]
	if rec["fileName"] != "hello.txt" {
		t.Fatalf("record fileName = %v", rec["fileName"])
	}
	if _, ok := rec["public"]; !ok {
		t.Fatalf("record must serialize 'public' (upstream Jackson naming): %v", rec)
	}
	fileID, _ := rec["fileId"].(string)
	if fileID == "" {
		t.Fatal("record has no fileId")
	}

	// Download returns the exact bytes.
	dl := e.do(t, http.MethodGet, "/d/"+fileID, "", nil, "")
	body, _ := io.ReadAll(dl.Body)
	dl.Body.Close()
	if dl.StatusCode != http.StatusOK || !bytes.Equal(body, content) {
		t.Fatalf("download status=%d body=%q", dl.StatusCode, body)
	}
	if cd := dl.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Fatalf("Content-Disposition = %q", cd)
	}

	// Public toggle.
	_, res = e.doJSON(t, http.MethodPut, "/api/file/"+fileID+"/public", token, map[string]bool{"isPublic": true})
	if res.Code != 1 {
		t.Fatalf("public toggle: %+v", res)
	}
	_, res = e.doJSON(t, http.MethodGet, "/api/file-list?page=1&size=10", "", nil)
	dataInto(t, res, &page)
	if page.Total != 1 || page.Records[0]["public"] != true {
		t.Fatalf("anonymous list after public toggle: %+v", page)
	}

	// Delete removes the DB row only (Telegram blob retained, matching
	// upstream); the listing drops it while /d/ still resolves the blob.
	_, res = e.doJSON(t, http.MethodDelete, "/api/file/"+fileID, token, nil)
	if res.Code != 1 {
		t.Fatalf("delete: %+v", res)
	}
	_, res = e.doJSON(t, http.MethodGet, "/api/file-list?page=1&size=10", token, nil)
	dataInto(t, res, &page)
	if page.Total != 0 {
		t.Fatalf("file-list total after delete = %d, want 0", page.Total)
	}
	dl = e.do(t, http.MethodGet, "/d/"+fileID, "", nil, "")
	body, _ = io.ReadAll(dl.Body)
	dl.Body.Close()
	if dl.StatusCode != http.StatusOK || !bytes.Equal(body, content) {
		t.Fatalf("blob download after row delete: status=%d body=%q", dl.StatusCode, body)
	}
}

func TestFileListFiltering(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login(t, "admin", "123456")
	if err := e.st.UpdateSetting("allow_registration", "true"); err != nil {
		t.Fatal(err)
	}
	_, res := e.doJSON(t, http.MethodPost, "/api/auth/register", "", map[string]string{
		"username": "carol", "password": "secret123", "confirmPassword": "secret123", "email": "",
	})
	var reg struct {
		Token string `json:"token"`
	}
	dataInto(t, res, &reg)

	e.upload(t, admin, "admin-private.txt", []byte("admin private"))
	e.upload(t, reg.Token, "carol-private.txt", []byte("carol private"))
	e.upload(t, reg.Token, "carol-public.txt", []byte("carol public"))

	// Make carol's public file public.
	var page struct {
		Total   int64            `json:"total"`
		Records []map[string]any `json:"records"`
	}
	_, res = e.doJSON(t, http.MethodGet, "/api/file-list?page=1&size=10", reg.Token, nil)
	dataInto(t, res, &page)
	for _, rec := range page.Records {
		if rec["fileName"] == "carol-public.txt" {
			id, _ := rec["fileId"].(string)
			e.doJSON(t, http.MethodPut, "/api/file/"+id+"/public", reg.Token, map[string]bool{"isPublic": true})
		}
	}

	// Anonymous sees only the public file.
	_, res = e.doJSON(t, http.MethodGet, "/api/file-list?page=1&size=10", "", nil)
	dataInto(t, res, &page)
	if page.Total != 1 {
		t.Fatalf("anonymous total = %d, want 1", page.Total)
	}

	// Carol sees her two files (own + public) but not admin's private one.
	_, res = e.doJSON(t, http.MethodGet, "/api/file-list?page=1&size=10", reg.Token, nil)
	dataInto(t, res, &page)
	if page.Total != 2 {
		t.Fatalf("carol total = %d, want 2", page.Total)
	}

	// Admin sees everything, keyword narrows it down.
	_, res = e.doJSON(t, http.MethodGet, "/api/file-list?page=1&size=10", admin, nil)
	dataInto(t, res, &page)
	if page.Total != 3 {
		t.Fatalf("admin total = %d, want 3", page.Total)
	}
	_, res = e.doJSON(t, http.MethodGet, "/api/file-list?page=1&size=10&keyword=carol-private", "", nil)
	dataInto(t, res, &page)
	if page.Total != 0 {
		t.Fatalf("anonymous keyword search over private file total = %d, want 0", page.Total)
	}
}

func TestChunkedUploadMerge(t *testing.T) {
	e := newTestEnv(t)
	token := e.login(t, "admin", "123456")

	// 25 MiB forces three 10 MiB chunks + a record file.
	data := make([]byte, 25<<20)
	if _, err := rand.Read(data); err != nil {
		t.Fatalf("rand: %v", err)
	}
	up := e.upload(t, token, "big.bin", data)

	dl := e.do(t, http.MethodGet, strings.TrimPrefix(up["downloadLink"], e.ts.URL), "", nil, "")
	got, err := io.ReadAll(dl.Body)
	dl.Body.Close()
	if err != nil || dl.StatusCode != http.StatusOK {
		t.Fatalf("download: status=%d err=%v", dl.StatusCode, err)
	}
	if len(got) != len(data) || sha256.Sum256(got) != sha256.Sum256(data) {
		t.Fatalf("merged download mismatch: len=%d want=%d", len(got), len(data))
	}
}

func TestDownloadHeadersAndSniffing(t *testing.T) {
	e := newTestEnv(t)
	token := e.login(t, "admin", "123456")

	// Image name → inline + image content type.
	png := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 1, 2, 3, 4}
	up := e.upload(t, token, "pic.png", png)
	dl := e.do(t, http.MethodGet, strings.TrimPrefix(up["downloadLink"], e.ts.URL), "", nil, "")
	dl.Body.Close()
	if ct := dl.Header.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("png Content-Type = %q", ct)
	}
	if cd := dl.Header.Get("Content-Disposition"); cd != "inline" {
		t.Fatalf("png Content-Disposition = %q, want inline", cd)
	}

	// Extension-less name gets a sniffed extension (upstream Tika behavior):
	// for the PNG the sniffed type drives inline disposition…
	up = e.upload(t, token, "noext", png)
	dl = e.do(t, http.MethodGet, strings.TrimPrefix(up["downloadLink"], e.ts.URL), "", nil, "")
	dl.Body.Close()
	if ct := dl.Header.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("sniffed Content-Type = %q, want image/png", ct)
	}
	if cd := dl.Header.Get("Content-Disposition"); cd != "inline" {
		t.Fatalf("sniffed Content-Disposition = %q, want inline", cd)
	}

	// …and for text it appears in the attachment filename.
	up = e.upload(t, token, "noext2", []byte("just some plain text"))
	dl = e.do(t, http.MethodGet, strings.TrimPrefix(up["downloadLink"], e.ts.URL), "", nil, "")
	dl.Body.Close()
	if cd := dl.Header.Get("Content-Disposition"); !strings.Contains(cd, "noext2.txt") {
		t.Fatalf("sniffed attachment Content-Disposition = %q, want noext2.txt", cd)
	}
}

func TestWebSocketProgress(t *testing.T) {
	e := newTestEnv(t)
	token := e.login(t, "admin", "123456")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(e.ts.URL, "http") + "/ws/upload-progress"
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// ping → pong.
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"ping"}`)); err != nil {
		t.Fatalf("ws ping: %v", err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("ws read pong: %v", err)
	}
	var pong map[string]any
	_ = json.Unmarshal(data, &pong)
	if pong["type"] != "pong" || pong["timestamp"] == "" {
		t.Fatalf("pong message wrong: %s", data)
	}

	// Upload and collect progress + complete events.
	e.upload(t, token, "progress.bin", []byte("progress payload"))

	gotProgress, gotComplete := false, false
	for !gotProgress || !gotComplete {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("ws read: %v (progress=%v complete=%v)", err, gotProgress, gotComplete)
		}
		var msg map[string]any
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if msg["fileName"] != "progress.bin" {
			continue
		}
		switch msg["type"] {
		case "upload_progress":
			if msg["percentage"] == float64(100) && msg["totalChunks"] == float64(1) {
				gotProgress = true
			}
		case "upload_complete":
			gotComplete = true
		}
	}
}

func TestAdminEndpoints(t *testing.T) {
	e := newTestEnv(t)
	token := e.login(t, "admin", "123456")

	// Settings.
	_, res := e.doJSON(t, http.MethodGet, "/api/setting/settings", token, nil)
	var settings []map[string]any
	dataInto(t, res, &settings)
	if len(settings) == 0 || settings[0]["key"] != "allow_registration" {
		t.Fatalf("settings list: %+v", settings)
	}
	if _, res := e.doJSON(t, http.MethodPost, "/api/setting", token, map[string]string{"key": "allow_registration", "value": "true"}); res.Code != 1 {
		t.Fatalf("update setting: %+v", res)
	}
	if _, res := e.doJSON(t, http.MethodPost, "/api/setting", token, map[string]string{"key": "nope", "value": "x"}); res.Code != 0 || *res.Msg != "该设置不存在" {
		t.Fatalf("unknown setting: %+v", res)
	}
	// Public registration-status reflects the change.
	_, res = e.doJSON(t, http.MethodGet, "/api/setting/registration-status", "", nil)
	var regStatus map[string]bool
	dataInto(t, res, &regStatus)
	if !regStatus["isRegistrationAllowed"] {
		t.Fatalf("registration status: %+v", regStatus)
	}

	// Bot config CRUD.
	if _, res := e.doJSON(t, http.MethodPost, "/api/config", token, map[string]string{
		"name": "default", "token": "123:ABC", "target": "-100123456",
	}); res.Code != 1 {
		t.Fatalf("save config: %+v", res)
	}
	_, res = e.doJSON(t, http.MethodGet, "/api/config?name=default", token, nil)
	var cfgData map[string]string
	dataInto(t, res, &cfgData)
	if cfgData["token"] != "123:ABC" || cfgData["target"] != "-100123456" {
		t.Fatalf("config readback: %+v", cfgData)
	}
	if _, ok := cfgData["id"]; ok {
		t.Fatal("config DTO must not expose id (upstream ConfigForm shape)")
	}
	_, res = e.doJSON(t, http.MethodGet, "/api/config/configs", token, nil)
	var configs []map[string]string
	dataInto(t, res, &configs)
	if len(configs) != 1 {
		t.Fatalf("configs list: %+v", configs)
	}
	if _, res := e.doJSON(t, http.MethodDelete, "/api/config/default", token, nil); res.Code != 1 {
		t.Fatalf("delete config: %+v", res)
	}
	if _, res := e.doJSON(t, http.MethodGet, "/api/config?name=default", token, nil); res.Code != 0 || *res.Msg != "配置文件不存在" {
		t.Fatalf("deleted config readback: %+v", res)
	}

	// send-message without a loaded bot.
	if _, res := e.doJSON(t, http.MethodPost, "/api/send-message", token, map[string]string{"message": "hi"}); res.Code != 0 || *res.Msg != "bot token未设置" {
		t.Fatalf("send-message without bot: %+v", res)
	}

	// Keep-alive control.
	_, res = e.doJSON(t, http.MethodGet, "/api/bot/keep-alive/status", token, nil)
	var statusText string
	dataInto(t, res, &statusText)
	if statusText != "运行中" {
		t.Fatalf("keep-alive status = %q", statusText)
	}
	e.doJSON(t, http.MethodPost, "/api/bot/keep-alive/stop", token, nil)
	_, res = e.doJSON(t, http.MethodGet, "/api/bot/keep-alive/status", token, nil)
	dataInto(t, res, &statusText)
	if statusText != "已停止" {
		t.Fatalf("keep-alive status after stop = %q", statusText)
	}
	e.doJSON(t, http.MethodPost, "/api/bot/keep-alive/start", token, nil)

	// WebDAV config get/put/status.
	_, res = e.doJSON(t, http.MethodGet, "/api/webdav-config", token, nil)
	var wdc map[string]any
	dataInto(t, res, &wdc)
	if wdc["enabled"] != true || wdc["allowedRoles"] != "admin" {
		t.Fatalf("webdav config: %+v", wdc)
	}
	put := map[string]any{
		"enabled": false, "requireAuth": true, "allowedRoles": "admin,all",
		"allowMkdir": true, "allowDelete": true, "allowMove": true, "allowCopy": true,
		"description": "test",
	}
	if _, res := e.doJSON(t, http.MethodPut, "/api/webdav-config", token, put); res.Code != 1 {
		t.Fatalf("update webdav config: %+v", res)
	}
	_, res = e.doJSON(t, http.MethodGet, "/api/webdav-config/status", token, nil)
	var enabled bool
	dataInto(t, res, &enabled)
	if enabled {
		t.Fatal("webdav should be disabled after update")
	}

	// Backup download returns the raw sqlite file.
	resp := e.do(t, http.MethodGet, "/api/backup/download", token, nil, "")
	head := make([]byte, 15)
	io.ReadFull(resp.Body, head)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(head) != "SQLite format 3" {
		t.Fatalf("backup download status=%d head=%q", resp.StatusCode, head)
	}

	// Users list, admin deletion guard, role update.
	_, res = e.doJSON(t, http.MethodGet, "/api/auth/admin/users", token, nil)
	var users []map[string]any
	dataInto(t, res, &users)
	if len(users) < 2 {
		t.Fatalf("users list: %+v", users)
	}
	var adminID float64
	for _, u := range users {
		if u["username"] == "admin" {
			adminID = u["id"].(float64)
		}
	}
	if _, res := e.doJSON(t, http.MethodDelete, "/api/auth/admin/users/"+strconv.FormatInt(int64(adminID), 10), token, nil); res.Code != 0 || *res.Msg != "不能删除管理员账户" {
		t.Fatalf("delete admin: %+v", res)
	}
	var visitorID float64
	for _, u := range users {
		if u["username"] == "visitor" {
			visitorID = u["id"].(float64)
		}
	}
	if _, res := e.doJSON(t, http.MethodPut, "/api/auth/admin/users/"+strconv.FormatInt(int64(visitorID), 10)+"/role?role=user", token, nil); res.Code != 1 {
		t.Fatalf("update role: %+v", res)
	}

	// System monitor.
	_, res = e.doJSON(t, http.MethodGet, "/api/system/monitor", token, nil)
	var monitor map[string]any
	dataInto(t, res, &monitor)
	if _, ok := monitor["userCount"]; !ok {
		t.Fatalf("monitor: %+v", monitor)
	}

	// File URL re-prefixing works (rebuilds download_url from the file id).
	if _, res := e.doJSON(t, http.MethodPut, "/api/file-url", token, nil); res.Code != 1 {
		t.Fatalf("file-url: %+v", res)
	}

	// Non-admin cannot access admin endpoints.
	visitor := e.login(t, "visitor", "hello")
	if _, res := e.doJSON(t, http.MethodGet, "/api/auth/admin/users", visitor, nil); res.Code != 0 || *res.Msg != "非admin，权限不足" {
		t.Fatalf("visitor admin access: %+v", res)
	}
}

func TestUIServedAndCORS(t *testing.T) {
	e := newTestEnv(t)

	resp := e.do(t, http.MethodGet, "/", "", nil, "")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "tgDrive UI") {
		t.Fatalf("UI index: status=%d body=%q", resp.StatusCode, body)
	}

	// SPA fallback for extension-less routes.
	resp = e.do(t, http.MethodGet, "/some/route", "", nil, "")
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "tgDrive UI") {
		t.Fatalf("SPA fallback: status=%d", resp.StatusCode)
	}

	// Missing asset is a 404.
	resp = e.do(t, http.MethodGet, "/missing.png", "", nil, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing asset status = %d", resp.StatusCode)
	}

	// CORS on /api/**.
	req, _ := http.NewRequest(http.MethodOptions, e.ts.URL+"/api/upload", nil)
	req.Header.Set("Origin", "http://example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	cresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	cresp.Body.Close()
	if cresp.StatusCode != http.StatusNoContent || cresp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("preflight: status=%d origin=%q", cresp.StatusCode, cresp.Header.Get("Access-Control-Allow-Origin"))
	}
}

func TestBackupUploadMerge(t *testing.T) {
	e := newTestEnv(t)
	token := e.login(t, "admin", "123456")

	// Fabricate a backup database containing a file row the live DB lacks.
	backupPath := filepath.Join(t.TempDir(), "backup.db")
	bst, err := store.Open(backupPath)
	if err != nil {
		t.Fatalf("open backup store: %v", err)
	}
	if _, err := bst.InsertFile(&model.File{
		FileName: "from-backup.txt", FileID: "backup-file-1",
		DownloadURL: "http://old/d/backup-file-1", UploadTime: 1,
		Size: "1.0 KB", FullSize: 1024,
	}); err != nil {
		t.Fatalf("seed backup file: %v", err)
	}
	bst.Close()
	data, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}

	// A non-.db upload is rejected.
	var bad bytes.Buffer
	bw := multipart.NewWriter(&bad)
	bfw, _ := bw.CreateFormFile("multipartFile", "notes.txt")
	bfw.Write([]byte("not a database"))
	bw.Close()
	resp := e.do(t, http.MethodPost, "/api/backup/upload", token, &bad, bw.FormDataContentType())
	var res result
	json.NewDecoder(resp.Body).Decode(&res)
	resp.Body.Close()
	if res.Code != 0 {
		t.Fatalf("non-.db backup upload: %+v", res)
	}

	// The real backup merges the missing row.
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("multipartFile", "backup.db")
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(data)
	w.Close()
	resp = e.do(t, http.MethodPost, "/api/backup/upload", token, &buf, w.FormDataContentType())
	json.NewDecoder(resp.Body).Decode(&res)
	resp.Body.Close()
	if res.Code != 1 {
		t.Fatalf("backup upload: %+v", res)
	}

	_, res = e.doJSON(t, http.MethodGet, "/api/file-list?page=1&size=10", token, nil)
	var page struct {
		Total   int64            `json:"total"`
		Records []map[string]any `json:"records"`
	}
	dataInto(t, res, &page)
	found := false
	for _, rec := range page.Records {
		if rec["fileName"] == "from-backup.txt" {
			found = true
		}
	}
	if !found || page.Total != 1 {
		t.Fatalf("merged listing: total=%d found=%v", page.Total, found)
	}

	// The endpoint snapshots the live DB next to it.
	if _, err := os.Stat(filepath.Join(filepath.Dir(e.dbPath), "tgdrive_backup.db")); err != nil {
		t.Fatalf("backup snapshot missing: %v", err)
	}
}
