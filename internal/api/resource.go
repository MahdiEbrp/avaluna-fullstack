package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/arena/avaluna/internal/database"
	"github.com/arena/avaluna/internal/httpx"
	"github.com/arena/avaluna/internal/middleware"
)

type FieldKind int

const (
	KindString FieldKind = iota
	KindInt
	KindFloat
	KindBool
	KindJSON
	KindSlug
)

type Field struct {
	Name     string
	Kind     FieldKind
	Required bool
	Default  any
	From     string
}

type Resource struct {
	Name       string
	Table      string
	Fields     []Field
	Select     string
	Searchable []string
	Filters    []string
	OrderBy    string
	SoftDelete bool
	Owner      string
	ReadRole   string
	WriteRole  string
	MaxLimit   int
}

func (rs *Resource) selectList() string {
	if rs.Select == "" {
		return "*"
	}
	return rs.Select
}

func (rs *Resource) order() string {
	if rs.OrderBy == "" {
		return "id DESC"
	}
	return rs.OrderBy
}

func (rs *Resource) List(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page, limit, offset := httpx.PageParams(r)
		if rs.MaxLimit > 0 && limit > rs.MaxLimit {
			limit = rs.MaxLimit
		}
		where, args := rs.whereClause(r)
		total, err := db.Count(r.Context(),
			fmt.Sprintf("SELECT COUNT(*) FROM %s %s", rs.Table, where), args...)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		order := rs.order()
		if s := r.URL.Query().Get("sort"); s != "" {
			if o := rs.sortClause(s); o != "" {
				order = o
			}
		}
		q := fmt.Sprintf("SELECT %s FROM %s %s ORDER BY %s LIMIT ? OFFSET ?",
			rs.selectList(), rs.Table, where, order)
		rows, err := db.Maps(r.Context(), q, append(args, limit, offset)...)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		httpx.List(w, rows, httpx.MakePagination(page, limit, total))
	}
}

func (rs *Resource) Get(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.PathValue("id")
		col := "id"
		if _, err := strconv.Atoi(key); err != nil {
			col = rs.lookupColumn()
			if col == "" {
				httpx.Fail(w, httpx.CodeValidation, "Invalid identifier")
				return
			}
		}
		where := fmt.Sprintf("WHERE %s = ?", col)
		if rs.SoftDelete {
			where += " AND deleted_at IS NULL"
		}
		row, err := db.MapOne(r.Context(),
			fmt.Sprintf("SELECT %s FROM %s %s", rs.selectList(), rs.Table, where), key)
		if errors.Is(err, database.ErrNotFound) {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		if !rs.canTouch(r, row) {
			httpx.Fail(w, httpx.CodeForbidden, "")
			return
		}
		httpx.OK(w, row)
	}
}

func (rs *Resource) Create(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := httpx.DecodeLoose(r, &body); err != nil {
			httpx.Fail(w, httpx.CodeValidation, err.Error())
			return
		}
		cols, vals, issues := rs.bind(body, true)
		if len(issues) > 0 {
			httpx.Fail(w, httpx.CodeValidation, "", issues...)
			return
		}
		if rs.Owner != "" && middleware.UserID(r.Context()) > 0 && !contains(cols, rs.Owner) {
			cols = append(cols, rs.Owner)
			vals = append(vals, middleware.UserID(r.Context()))
		}
		if len(cols) == 0 {
			httpx.Fail(w, httpx.CodeValidation, "No writable fields supplied")
			return
		}
		ph := strings.TrimSuffix(strings.Repeat("?,", len(cols)), ",")
		res, err := db.Write(r.Context(),
			fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", rs.Table, strings.Join(cols, ","), ph), vals...)
		if err != nil {
			httpx.Fail(w, conflictOr(err), err.Error())
			return
		}
		id, _ := res.LastInsertId()
		row, _ := db.MapOne(r.Context(),
			fmt.Sprintf("SELECT %s FROM %s WHERE id = ?", rs.selectList(), rs.Table), id)
		httpx.Created(w, row)
	}
}

func (rs *Resource) Update(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := httpx.PathInt(r, "id")
		if !ok {
			httpx.Fail(w, httpx.CodeValidation, "Invalid id")
			return
		}
		existing, err := db.MapOne(r.Context(), fmt.Sprintf("SELECT * FROM %s WHERE id = ?", rs.Table), id)
		if errors.Is(err, database.ErrNotFound) {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		if !rs.canTouch(r, existing) {
			httpx.Fail(w, httpx.CodeForbidden, "")
			return
		}
		var body map[string]any
		if err := httpx.DecodeLoose(r, &body); err != nil {
			httpx.Fail(w, httpx.CodeValidation, err.Error())
			return
		}
		cols, vals, issues := rs.bind(body, false)
		if len(issues) > 0 {
			httpx.Fail(w, httpx.CodeValidation, "", issues...)
			return
		}
		if len(cols) == 0 {
			httpx.Fail(w, httpx.CodeValidation, "No fields to update")
			return
		}
		sets := make([]string, 0, len(cols)+1)
		for _, c := range cols {
			sets = append(sets, c+" = ?")
		}
		if rs.hasColumn("updated_at") {
			sets = append(sets, "updated_at = datetime('now')")
		}
		vals = append(vals, id)
		if _, err := db.Write(r.Context(),
			fmt.Sprintf("UPDATE %s SET %s WHERE id = ?", rs.Table, strings.Join(sets, ", ")), vals...); err != nil {
			httpx.Fail(w, conflictOr(err), err.Error())
			return
		}
		row, _ := db.MapOne(r.Context(),
			fmt.Sprintf("SELECT %s FROM %s WHERE id = ?", rs.selectList(), rs.Table), id)
		httpx.Success(w, http.StatusOK, "Updated", row, nil)
	}
}

func (rs *Resource) Delete(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := httpx.PathInt(r, "id")
		if !ok {
			httpx.Fail(w, httpx.CodeValidation, "Invalid id")
			return
		}
		existing, err := db.MapOne(r.Context(), fmt.Sprintf("SELECT * FROM %s WHERE id = ?", rs.Table), id)
		if errors.Is(err, database.ErrNotFound) {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		if !rs.canTouch(r, existing) {
			httpx.Fail(w, httpx.CodeForbidden, "")
			return
		}
		q := fmt.Sprintf("DELETE FROM %s WHERE id = ?", rs.Table)
		if rs.SoftDelete {
			q = fmt.Sprintf("UPDATE %s SET deleted_at = datetime('now') WHERE id = ?", rs.Table)
		}
		if _, err := db.Write(r.Context(), q, id); err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		httpx.NoBody(w, "Deleted")
	}
}

func (rs *Resource) lookupColumn() string {
	for _, c := range []string{"slug", "code", "key"} {
		if rs.hasColumn(c) {
			return c
		}
	}
	return ""
}

func (rs *Resource) hasColumn(name string) bool {
	for _, f := range rs.Fields {
		if f.Name == name {
			return true
		}
	}
	return name == "updated_at" && rs.touchUpdatedAt()
}

func (rs *Resource) whereClause(r *http.Request) (string, []any) {
	conds := []string{}
	args := []any{}
	if rs.SoftDelete {
		conds = append(conds, "deleted_at IS NULL")
	}
	q := r.URL.Query()
	for _, f := range rs.Filters {
		if v := q.Get(f); v != "" {
			conds = append(conds, f+" = ?")
			args = append(args, v)
		}
	}
	if term := strings.TrimSpace(q.Get("q")); term != "" && len(rs.Searchable) > 0 {
		or := make([]string, 0, len(rs.Searchable))
		for _, c := range rs.Searchable {
			or = append(or, c+" LIKE ?")
			args = append(args, "%"+term+"%")
		}
		conds = append(conds, "("+strings.Join(or, " OR ")+")")
	}

	if rs.Owner != "" {
		if uid := middleware.UserID(r.Context()); uid > 0 && !isStaff(r) {
			conds = append(conds, rs.Owner+" = ?")
			args = append(args, uid)
		}
	}
	if len(conds) == 0 {
		return "", args
	}
	return "WHERE " + strings.Join(conds, " AND "), args
}

func (rs *Resource) sortClause(s string) string {
	dir := "ASC"
	col := s
	if strings.HasPrefix(s, "-") {
		dir, col = "DESC", s[1:]
	}
	allowed := map[string]bool{"id": true, "created_at": true, "updated_at": true}
	for _, f := range rs.Fields {
		allowed[f.Name] = true
	}
	for _, f := range rs.Filters {
		allowed[f] = true
	}
	if !allowed[col] {
		return ""
	}
	return col + " " + dir
}

func (rs *Resource) canTouch(r *http.Request, row map[string]any) bool {
	if rs.Owner == "" || isStaff(r) {
		return true
	}
	uid := middleware.UserID(r.Context())
	if uid == 0 {
		return false
	}
	switch v := row[rs.Owner].(type) {
	case int64:
		return int(v) == uid
	case int:
		return v == uid
	case nil:
		return false
	}
	return false
}

func isStaff(r *http.Request) bool {
	role := middleware.Role(r.Context())
	return role == "admin" || role == "super_admin" || role == "support" || role == "editor"
}

func (rs *Resource) bind(body map[string]any, create bool) ([]string, []any, []httpx.FieldIssue) {
	cols := []string{}
	vals := []any{}
	issues := []httpx.FieldIssue{}
	for _, f := range rs.Fields {
		raw, present := body[f.Name]
		if !present {
			if create && f.Required {
				if f.Kind == KindSlug && f.From != "" {
					if src, ok := body[f.From]; ok {
						cols = append(cols, f.Name)
						vals = append(vals, Slugify(toString(src)))
						continue
					}
				}
				if f.Default != nil {
					cols = append(cols, f.Name)
					vals = append(vals, f.Default)
					continue
				}
				issues = append(issues, httpx.FieldIssue{Field: f.Name, Issue: "is required"})
			}
			continue
		}
		v, err := coerce(f, raw)
		if err != nil {
			issues = append(issues, httpx.FieldIssue{Field: f.Name, Issue: err.Error()})
			continue
		}
		if f.Required && create && isEmpty(v) {
			issues = append(issues, httpx.FieldIssue{Field: f.Name, Issue: "must not be empty"})
			continue
		}
		cols = append(cols, f.Name)
		vals = append(vals, v)
	}
	return cols, vals, issues
}

func coerce(f Field, raw any) (any, error) {
	switch f.Kind {
	case KindInt:
		switch t := raw.(type) {
		case float64:
			return int64(t), nil
		case int:
			return int64(t), nil
		case int64:
			return t, nil
		case string:
			if t == "" {
				return int64(0), nil
			}
			n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
			if err != nil {
				return nil, errors.New("must be an integer")
			}
			return n, nil
		case bool:
			if t {
				return int64(1), nil
			}
			return int64(0), nil
		}
		return nil, errors.New("must be an integer")
	case KindFloat:
		switch t := raw.(type) {
		case float64:
			return t, nil
		case string:
			if t == "" {
				return 0.0, nil
			}
			n, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
			if err != nil {
				return nil, errors.New("must be a number")
			}
			return n, nil
		}
		return nil, errors.New("must be a number")
	case KindBool:
		switch t := raw.(type) {
		case bool:
			if t {
				return int64(1), nil
			}
			return int64(0), nil
		case float64:
			if t != 0 {
				return int64(1), nil
			}
			return int64(0), nil
		case string:
			s := strings.ToLower(strings.TrimSpace(t))
			if s == "1" || s == "true" || s == "on" || s == "yes" {
				return int64(1), nil
			}
			return int64(0), nil
		}
		return int64(0), nil
	case KindSlug:
		return Slugify(toString(raw)), nil
	case KindJSON:
		s := toString(raw)
		if s == "" {
			s = "{}"
		}
		return s, nil
	default:
		return toString(raw), nil
	}
}

func toString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return fmt.Sprint(t)
	}
}

func isEmpty(v any) bool {
	s, ok := v.(string)
	return ok && strings.TrimSpace(s) == ""
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func conflictOr(err error) string {
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "unique") || strings.Contains(msg, "constraint") {
		return httpx.CodeConflict
	}
	return httpx.CodeInternal
}

func Slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r > 127:
			if r != '؟' && r != '،' {
				b.WriteRune(r)
				prevDash = false
			}
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteRune('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

var resourceUpdatedTables = map[string]bool{}

func MarkUpdatedAt(tables ...string) {
	for _, t := range tables {
		resourceUpdatedTables[t] = true
	}
}

func (rs *Resource) touchUpdatedAt() bool { return resourceUpdatedTables[rs.Table] }

func (rs *Resource) Mount(reg Registrar, db *database.DB) {
	base := "/" + strings.TrimPrefix(rs.Name, "/")
	reg.Handle(http.MethodGet, base, rs.ReadRole, rs.List(db))
	reg.Handle(http.MethodGet, base+"/{id}", rs.ReadRole, rs.Get(db))
	reg.Handle(http.MethodPost, base, rs.WriteRole, rs.Create(db))
	reg.Handle(http.MethodPut, base+"/{id}", rs.WriteRole, rs.Update(db))
	reg.Handle(http.MethodPatch, base+"/{id}", rs.WriteRole, rs.Update(db))
	reg.Handle(http.MethodDelete, base+"/{id}", rs.WriteRole, rs.Delete(db))
}

type Registrar interface {
	Handle(method, pattern, role string, h http.HandlerFunc)
}

func scanIDs(ctx context.Context, db *database.DB, q string, args ...any) ([]int, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []int{}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
