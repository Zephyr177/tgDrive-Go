// Package model defines the persistent domain types shared across tgDrive.
package model

// User is a tgDrive account. Roles used upstream: "admin", "user", "visitor".
type User struct {
	ID            int64  `json:"id"`
	Username      string `json:"username"`
	Password      string `json:"-"` // hash, never serialized
	Role          string `json:"role"`
	Email         string `json:"email"`
	LastLoginTime string `json:"lastLoginTime"`
}

// File is one stored object (backed by a Telegram file id) or a directory
// entry used by the WebDAV layer. JSON names match the upstream wire format.
type File struct {
	ID          int64  `json:"id"`
	FileName    string `json:"fileName"`
	DownloadURL string `json:"downloadUrl"`
	UploadTime  int64  `json:"uploadTime"` // unix seconds
	Size        string `json:"size"`       // human readable
	FullSize    int64  `json:"fullSize"`   // bytes
	FileID      string `json:"fileId"`
	MessageID   *int64 `json:"messageId"`
	WebDAVPath  string `json:"webdavPath"`
	Dir         bool   `json:"dir"`
	UserID      *int64 `json:"userId"`
	IsPublic    bool   `json:"public"`
	Uploader    string `json:"uploader"` // filled by listings (join)
}
