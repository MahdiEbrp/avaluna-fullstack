package core

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/arena/avaluna/internal/config"
	"github.com/arena/avaluna/internal/database"
	"github.com/arena/avaluna/internal/middleware"
)

type Scheduler interface {
	RunNow(ctx context.Context, name string) (string, error)
	SetEnabled(ctx context.Context, name string, enabled bool) error
	Reschedule(ctx context.Context, name, spec string) error
	Registry() []JobMeta
}

type JobMeta struct {
	Name        string `json:"name"`
	Schedule    string `json:"schedule"`
	Description string `json:"description"`
	Retry       string `json:"retryPolicy"`
}

type Enqueuer interface {
	Enqueue(ctx context.Context, queue, name string, payload any, maxAttempts int) (int64, error)
	Stats(ctx context.Context) (map[string]map[string]int, error)
}

type Core struct {
	Cfg   *config.Config
	DB    *database.DB
	Log   *slog.Logger
	RL    *middleware.RateLimiter
	Cron  Scheduler
	Queue Enqueuer

	StartedAt time.Time

	settingsMu sync.RWMutex
	settings   map[string]string

	scopes map[string]string

	cacheMu sync.RWMutex
	cache   map[string]cacheEntry
}

type cacheEntry struct {
	val    any
	expiry time.Time
}

func New(cfg *config.Config, db *database.DB, log *slog.Logger) *Core {
	c := &Core{
		Cfg:       cfg,
		DB:        db,
		Log:       log,
		RL:        middleware.NewRateLimiter(cfg.RateLimitEnabled),
		StartedAt: time.Now().UTC(),
		settings:  map[string]string{},
		cache:     map[string]cacheEntry{},
	}
	_ = c.ReloadSettings(context.Background())
	return c
}

func (c *Core) ReloadSettings(ctx context.Context) error {
	rows, err := c.DB.Maps(ctx, "SELECT key, value, scope FROM settings")
	if err != nil {
		return err
	}
	m := make(map[string]string, len(rows))
	sc := make(map[string]string, len(rows))
	for _, r := range rows {
		k, _ := r["key"].(string)
		v, _ := r["value"].(string)
		s, _ := r["scope"].(string)
		if s == "" {
			s = "public"
		}
		m[k] = v
		sc[k] = s
	}
	c.settingsMu.Lock()
	c.settings = m
	c.scopes = sc
	c.settingsMu.Unlock()
	return nil
}

func (c *Core) Setting(key, def string) string {
	c.settingsMu.RLock()
	defer c.settingsMu.RUnlock()
	if v, ok := c.settings[key]; ok && v != "" {
		return v
	}
	return def
}

func (c *Core) SettingBool(key string, def bool) bool {
	v := c.Setting(key, "")
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func (c *Core) SettingInt(key string, def int) int {
	v := c.Setting(key, "")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func (c *Core) SetSetting(ctx context.Context, key, value, scope string) error {
	if scope == "" {
		scope = "private"
	}

	if _, err := c.DB.Write(ctx, `INSERT INTO settings(key,value,scope,updated_at)
		VALUES(?,?,?,datetime('now'))
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=datetime('now')`,
		key, value, scope); err != nil {
		return err
	}
	c.cacheSetting(ctx, key, value, scope)
	return nil
}

func (c *Core) SetSettingScope(ctx context.Context, key, scope string) error {
	if scope == "" {
		scope = "private"
	}
	if _, err := c.DB.Write(ctx,
		`UPDATE settings SET scope=?, updated_at=datetime('now') WHERE key=?`, scope, key); err != nil {
		return err
	}
	c.settingsMu.Lock()
	if c.scopes == nil {
		c.scopes = map[string]string{}
	}
	c.scopes[key] = scope
	c.settingsMu.Unlock()
	return nil
}

func (c *Core) cacheSetting(ctx context.Context, key, value, fallback string) {
	effective := fallback
	if s, err := c.DB.Scalar(ctx, `SELECT scope FROM settings WHERE key=?`, key); err == nil && s.Valid && s.String != "" {
		effective = s.String
	}
	c.settingsMu.Lock()
	c.settings[key] = value
	if c.scopes == nil {
		c.scopes = map[string]string{}
	}
	c.scopes[key] = effective
	c.settingsMu.Unlock()
}

func (c *Core) AllSettings(scope string) map[string]string {
	c.settingsMu.RLock()
	defer c.settingsMu.RUnlock()
	out := make(map[string]string, len(c.settings))
	for k, v := range c.settings {
		if scope != "" {
			ks := c.scopes[k]
			if ks == "" {

				ks = "private"
			}
			if ks != scope {
				continue
			}
		}
		out[k] = v
	}
	return out
}

func (c *Core) MaintenanceMode() bool { return c.SettingBool("maintenance_mode", false) }

func (c *Core) CacheGet(key string) (any, bool) {
	c.cacheMu.RLock()
	e, ok := c.cache[key]
	c.cacheMu.RUnlock()
	if !ok || time.Now().After(e.expiry) {
		return nil, false
	}
	return e.val, true
}

func (c *Core) CacheSet(key string, val any, ttl time.Duration) {
	c.cacheMu.Lock()
	c.cache[key] = cacheEntry{val: val, expiry: time.Now().Add(ttl)}
	c.cacheMu.Unlock()
}

func (c *Core) CacheClear(prefix string) int {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	n := 0
	for k := range c.cache {
		if prefix == "" || len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(c.cache, k)
			n++
		}
	}
	return n
}

func (c *Core) CacheSize() int {
	c.cacheMu.RLock()
	defer c.cacheMu.RUnlock()
	return len(c.cache)
}

func (c *Core) Audit(ctx context.Context, actorID int, role, action, entity, entityID, ip string, meta any) {
	b, _ := json.Marshal(meta)
	if _, err := c.DB.Write(ctx,
		`INSERT INTO audit_log(actor_id,actor_role,action,entity,entity_id,ip,meta_json)
		 VALUES(?,?,?,?,?,?,?)`,
		nullableInt(actorID), role, action, entity, entityID, ip, string(b)); err != nil {
		c.Log.Warn("audit write failed", slog.String("err", err.Error()))
	}
}

func (c *Core) LogError(ctx context.Context, level, code, message, path, stack string) {
	if _, err := c.DB.Write(ctx,
		`INSERT INTO error_log(level,code,message,path,stack) VALUES(?,?,?,?,?)`,
		level, code, message, path, stack); err != nil {
		c.Log.Warn("error log write failed", slog.String("err", err.Error()))
	}
}

func (c *Core) Notify(ctx context.Context, userID int, typ, title, body, link string) {
	if userID <= 0 {
		return
	}
	if _, err := c.DB.Write(ctx,
		`INSERT INTO notifications(user_id,type,title,body,link) VALUES(?,?,?,?,?)`,
		userID, typ, title, body, link); err != nil {
		c.Log.Warn("notification write failed", slog.String("err", err.Error()))
	}
}

func (c *Core) Uptime() time.Duration { return time.Since(c.StartedAt) }

func nullableInt(v int) any {
	if v <= 0 {
		return nil
	}
	return v
}
