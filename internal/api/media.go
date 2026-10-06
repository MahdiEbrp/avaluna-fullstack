package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/arena/avaluna/internal/auth"
	"github.com/arena/avaluna/internal/core"
	"github.com/arena/avaluna/internal/httpx"
	"github.com/arena/avaluna/internal/middleware"
)

const maxUploadBytes = 10 << 20

var allowedMime = map[string]string{
	"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp",
	"image/gif": ".gif", "image/svg+xml": ".svg", "application/pdf": ".pdf",
	"text/csv": ".csv", "application/vnd.ms-excel": ".csv",
}

func registerMediaModule(rt *Router, c *core.Core) {
	rt.Module("16-media")

	rt.Handle(http.MethodPost, "/media/upload", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
			httpx.Fail(w, httpx.CodeValidation, "Invalid multipart form: "+err.Error())
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			httpx.Fail(w, httpx.CodeValidation, "file field is required")
			return
		}
		defer func() { _ = file.Close() }()
		rec, err := storeUpload(c, r, file, header, r.FormValue("alt"))
		if err != nil {
			httpx.Fail(w, httpx.CodeValidation, err.Error())
			return
		}
		writeCreated(w, rec)
	})

	rt.Handle(http.MethodPost, "/media/bulk-upload", roleVendor, func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(5 * maxUploadBytes); err != nil {
			httpx.Fail(w, httpx.CodeValidation, err.Error())
			return
		}
		files := r.MultipartForm.File["files"]
		if len(files) == 0 {
			files = r.MultipartForm.File["file"]
		}
		out := []map[string]any{}
		failed := []string{}
		for _, fh := range files {
			f, err := fh.Open()
			if err != nil {
				failed = append(failed, fh.Filename)
				continue
			}
			rec, err := storeUpload(c, r, f, fh, "")
			_ = f.Close()
			if err != nil {
				failed = append(failed, fh.Filename+": "+err.Error())
				continue
			}
			out = append(out, rec)
		}
		httpx.Success(w, http.StatusCreated,
			fmt.Sprintf("%d uploaded, %d failed", len(out), len(failed)),
			map[string]any{"uploaded": out, "failed": failed}, nil)
	})

	rt.Handle(http.MethodGet, "/media", roleEditor, func(w http.ResponseWriter, r *http.Request) {
		page, limit, offset := paginate(r)
		total, _ := c.DB.Count(r.Context(), `SELECT COUNT(*) FROM media`)
		rows, _ := c.DB.Maps(r.Context(),
			`SELECT id,uuid,filename,mime,size_bytes,url,alt,created_at FROM media ORDER BY id DESC LIMIT ? OFFSET ?`,
			limit, offset)
		httpx.List(w, rows, httpx.MakePagination(page, limit, total))
	})

	rt.Handle(http.MethodGet, "/media/{id}", "", func(w http.ResponseWriter, r *http.Request) {
		key := r.PathValue("id")
		row, err := c.DB.MapOne(r.Context(), `SELECT * FROM media WHERE id=? OR uuid=?`, key, key)
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		writeOK(w, row)
	})

	rt.Handle(http.MethodDelete, "/media/{id}", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		key := r.PathValue("id")
		row, err := c.DB.MapOne(r.Context(), `SELECT * FROM media WHERE id=? OR uuid=?`, key, key)
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		if asInt(row["owner_id"]) != middleware.UserID(r.Context()) && !isStaff(r) {
			httpx.Fail(w, httpx.CodeForbidden, "")
			return
		}
		_ = os.Remove(asStr(row["path"]))
		_, _ = c.DB.Write(r.Context(), `DELETE FROM media WHERE id=?`, row["id"])
		audit(c, r, "media.delete", "media", asStr(row["id"]), nil)
		httpx.NoBody(w, "Media deleted")
	})
}

func storeUpload(c *core.Core, r *http.Request, file multipart.File,
	header *multipart.FileHeader, alt string) (map[string]any, error) {
	if header.Size > maxUploadBytes {
		return nil, fmt.Errorf("file exceeds the %d MiB limit", maxUploadBytes>>20)
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(io.LimitReader(file, 512), head)
	head = head[:n]
	mime := http.DetectContentType(head)
	mime = strings.Split(mime, ";")[0]
	ext, ok := allowedMime[mime]
	if !ok {

		switch strings.ToLower(filepath.Ext(header.Filename)) {
		case ".csv":
			mime, ext, ok = "text/csv", ".csv", true
		case ".svg":
			mime, ext, ok = "image/svg+xml", ".svg", true
		}
	}
	if !ok {
		return nil, fmt.Errorf("unsupported file type %q", mime)
	}

	if err := scanUpload(mime, head); err != nil {
		return nil, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	uid := auth.RandomToken(12)
	sub := time.Now().UTC().Format("2006/01")
	dir := filepath.Join(c.Cfg.UploadDir, sub)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	name := uid + ext
	full := filepath.Join(dir, name)
	dst, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return nil, err
	}
	hasher := sha256.New()
	size, err := io.Copy(io.MultiWriter(dst, hasher), io.LimitReader(file, maxUploadBytes))
	_ = dst.Close()
	if err != nil {
		_ = os.Remove(full)
		return nil, err
	}
	url := "/uploads/" + sub + "/" + name
	res, err := c.DB.Write(r.Context(),
		`INSERT INTO media(uuid,filename,mime,size_bytes,path,url,alt,owner_id,scanned)
		 VALUES(?,?,?,?,?,?,?,?,1)`,
		uid, header.Filename, mime, size, full, url, alt, nullIf(middleware.UserID(r.Context())))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return map[string]any{
		"id": id, "uuid": uid, "filename": header.Filename, "mime": mime,
		"size": size, "url": url, "checksum": hex.EncodeToString(hasher.Sum(nil)),
	}, nil
}

func scanUpload(mime string, head []byte) error {
	lower := bytes.ToLower(head)
	if bytes.Contains(lower, []byte("<?php")) || bytes.HasPrefix(head, []byte{0x7f, 'E', 'L', 'F'}) {
		return fmt.Errorf("executable content is not allowed")
	}
	if mime == "image/svg+xml" && (bytes.Contains(lower, []byte("<script")) ||
		bytes.Contains(lower, []byte("onload="))) {
		return fmt.Errorf("SVG contains active content")
	}
	if mime == "text/csv" && len(head) > 0 {
		switch head[0] {
		case '=', '+', '-', '@':
			return fmt.Errorf("CSV starts with a formula character (possible CSV injection)")
		}
	}
	return nil
}
