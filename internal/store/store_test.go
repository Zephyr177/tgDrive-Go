package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Zephyr177/tgDrive-Go/internal/auth"
	"github.com/Zephyr177/tgDrive-Go/internal/model"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "tgdrive.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestSeedDefaults(t *testing.T) {
	st := openTestStore(t)

	admin, err := st.UserByUsername("admin")
	if err != nil {
		t.Fatalf("admin missing: %v", err)
	}
	if admin.Role != "admin" || !auth.CheckPassword(admin.Password, "123456") {
		t.Errorf("admin seed wrong: role=%q password verifies=%v", admin.Role, auth.CheckPassword(admin.Password, "123456"))
	}

	visitor, err := st.UserByUsername("visitor")
	if err != nil {
		t.Fatalf("visitor missing: %v", err)
	}
	if visitor.Role != "visitor" || !auth.CheckPassword(visitor.Password, "hello") {
		t.Errorf("visitor seed wrong: role=%q", visitor.Role)
	}

	if v, err := st.GetSetting("allow_registration"); err != nil || v != "false" {
		t.Errorf("allow_registration = %q err=%v, want false", v, err)
	}

	cfg, err := st.GetWebDAVConfig()
	if err != nil {
		t.Fatalf("GetWebDAVConfig: %v", err)
	}
	if cfg.ID != 1 || !cfg.Enabled || !cfg.RequireAuth || cfg.AllowedRoles != "admin" {
		t.Errorf("webdav config defaults wrong: %+v", cfg)
	}
}

func TestFileCRUDAndFiltering(t *testing.T) {
	st := openTestStore(t)

	uid, err := st.CreateUser("alice", "hash", "user", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	insert := func(name, fileID string, userID *int64, public bool) *model.File {
		t.Helper()
		f := &model.File{
			FileName: name, FileID: fileID, DownloadURL: "http://old/d/" + fileID,
			UploadTime: 100, Size: "1.0 KB", FullSize: 1024, UserID: userID, IsPublic: public,
		}
		id, err := st.InsertFile(f)
		if err != nil {
			t.Fatalf("InsertFile: %v", err)
		}
		f.ID = id
		return f
	}
	fPrivate := insert("private.txt", "id-1", &uid, false)
	fPublic := insert("public.txt", "id-2", &uid, true)
	insert("anon.txt", "id-3", nil, true)

	// Admin sees everything.
	total, files, err := st.ListFilesPaged(FileFilter{Role: "admin", Page: 1, Size: 10})
	if err != nil || total != 3 || len(files) != 3 {
		t.Fatalf("admin list total=%d len=%d err=%v, want 3/3", total, len(files), err)
	}

	// A user sees own + public files: 2 own + 1 anonymous public = 3.
	total, _, err = st.ListFilesPaged(FileFilter{Role: "user", UserID: &uid, Page: 1, Size: 10})
	if err != nil || total != 3 {
		t.Fatalf("user list total=%d err=%v, want 3", total, err)
	}

	// Anonymous/visitor sees public only.
	total, _, err = st.ListFilesPaged(FileFilter{Role: "visitor", Page: 1, Size: 10})
	if err != nil || total != 2 {
		t.Fatalf("visitor list total=%d err=%v, want 2", total, err)
	}

	// Keyword search.
	total, files, err = st.ListFilesPaged(FileFilter{Role: "admin", Keyword: "private", Page: 1, Size: 10})
	if err != nil || total != 1 || files[0].ID != fPrivate.ID {
		t.Fatalf("keyword search total=%d err=%v, want only private.txt", total, err)
	}

	// Admin filtering by uploader.
	total, _, err = st.ListFilesPaged(FileFilter{Role: "admin", FilterUserID: &uid, Page: 1, Size: 10})
	if err != nil || total != 2 {
		t.Fatalf("admin uploader filter total=%d err=%v, want 2", total, err)
	}

	// Telegram file id lookup + uploader join.
	got, err := st.FileByFileID("id-2")
	if err != nil || got.Uploader != "alice" {
		t.Fatalf("FileByFileID err=%v uploader=%q, want alice", err, got.Uploader)
	}

	// Download URL prefix rewrite rebuilds from the file id.
	if err := st.UpdateDownloadURLPrefix("https://new.example"); err != nil {
		t.Fatalf("UpdateDownloadURLPrefix: %v", err)
	}
	got, _ = st.FileByID(fPrivate.ID)
	if got.DownloadURL != "https://new.example/d/id-1" {
		t.Errorf("DownloadURL = %q, want https://new.example/d/id-1", got.DownloadURL)
	}

	// Public toggle + delete.
	if err := st.UpdateFilePublic(fPrivate.ID, true); err != nil {
		t.Fatalf("UpdateFilePublic: %v", err)
	}
	if got, _ = st.FileByID(fPrivate.ID); !got.IsPublic {
		t.Error("public flag not updated")
	}
	if err := st.DeleteFile(fPublic.ID); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if _, err := st.FileByID(fPublic.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("deleted file still present, err=%v", err)
	}
}

func TestUpdateSettingSemantics(t *testing.T) {
	st := openTestStore(t)
	if err := st.UpdateSetting("allow_registration", "true"); err != nil {
		t.Fatalf("update existing setting: %v", err)
	}
	if v, _ := st.GetSetting("allow_registration"); v != "true" {
		t.Errorf("setting not updated: %q", v)
	}
	if err := st.UpdateSetting("missing_key", "x"); err == nil {
		t.Error("updating a missing setting must fail")
	}
}
