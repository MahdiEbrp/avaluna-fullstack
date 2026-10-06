package core_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/arena/avaluna/internal/testsupport"
)

func TestSettingAccessorsCoerceTypes(t *testing.T) {
	env := testsupport.New(t)
	c := env.Core
	ctx := context.Background()

	if err := c.SetSetting(ctx, "unit_text", "hello", "general"); err != nil {
		t.Fatalf("set text: %v", err)
	}
	if err := c.SetSetting(ctx, "unit_true", "true", "general"); err != nil {
		t.Fatalf("set bool: %v", err)
	}
	if err := c.SetSetting(ctx, "unit_num", "42", "general"); err != nil {
		t.Fatalf("set int: %v", err)
	}
	if err := c.SetSetting(ctx, "unit_junk", "not-a-number", "general"); err != nil {
		t.Fatalf("set junk: %v", err)
	}

	if got := c.Setting("unit_text", "fallback"); got != "hello" {
		t.Errorf("Setting = %q", got)
	}
	if got := c.Setting("unit_missing", "fallback"); got != "fallback" {
		t.Errorf("missing Setting = %q, want the default", got)
	}
	if !c.SettingBool("unit_true", false) {
		t.Error("SettingBool did not read true")
	}
	if c.SettingBool("unit_missing", false) {
		t.Error("missing SettingBool should use the default")
	}
	if !c.SettingBool("unit_missing", true) {
		t.Error("missing SettingBool should honour a true default")
	}
	if got := c.SettingInt("unit_num", 0); got != 42 {
		t.Errorf("SettingInt = %d, want 42", got)
	}

	if got := c.SettingInt("unit_junk", 7); got != 7 {
		t.Errorf("SettingInt on junk = %d, want the default 7", got)
	}
	if got := c.SettingBool("unit_junk", true); !got {
		t.Error("SettingBool on junk should use the default")
	}
}

func TestSetSettingIsVisibleImmediately(t *testing.T) {
	env := testsupport.New(t)
	c := env.Core
	ctx := context.Background()

	if err := c.SetSetting(ctx, "site_name", "Renamed Store", "general"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got := c.Setting("site_name", ""); got != "Renamed Store" {
		t.Errorf("Setting after write = %q, cache was not refreshed", got)
	}

	if err := c.ReloadSettings(ctx); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := c.Setting("site_name", ""); got != "Renamed Store" {
		t.Errorf("Setting after reload = %q, the write did not persist", got)
	}
}

func TestMaintenanceModeReflectsTheSetting(t *testing.T) {
	env := testsupport.New(t)
	c := env.Core
	ctx := context.Background()

	if c.MaintenanceMode() {
		t.Error("a freshly seeded store should not be in maintenance mode")
	}
	if err := c.SetSetting(ctx, "maintenance_mode", "true", "system"); err != nil {
		t.Fatalf("enable maintenance: %v", err)
	}
	if !c.MaintenanceMode() {
		t.Error("maintenance mode did not turn on")
	}
	if err := c.SetSetting(ctx, "maintenance_mode", "false", "system"); err != nil {
		t.Fatalf("disable maintenance: %v", err)
	}
	if c.MaintenanceMode() {
		t.Error("maintenance mode did not turn off")
	}
}

func TestAllSettingsFiltersByScope(t *testing.T) {
	env := testsupport.New(t)
	c := env.Core
	ctx := context.Background()

	if err := c.SetSetting(ctx, "unit_public", "shown", "public"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := c.SetSetting(ctx, "unit_private", "hidden", "private"); err != nil {
		t.Fatalf("set: %v", err)
	}

	pub := c.AllSettings("public")
	if pub["unit_public"] != "shown" {
		t.Errorf("a public setting was missing from the public map: %v", pub["unit_public"])
	}
	if _, leaked := pub["unit_private"]; leaked {
		t.Error("a private setting leaked into the public map")
	}

	if _, leaked := pub["webhook_secret"]; leaked {
		t.Error("webhook_secret leaked into the public settings map")
	}

	priv := c.AllSettings("private")
	if priv["unit_private"] != "hidden" {
		t.Error("a private setting was missing from the private map")
	}
	if _, wrong := priv["unit_public"]; wrong {
		t.Error("a public setting appeared in the private map")
	}

	all := c.AllSettings("")
	if len(all) <= len(pub) {
		t.Errorf("unfiltered AllSettings returned %d entries, public returned %d", len(all), len(pub))
	}
	if all["unit_private"] != "hidden" || all["unit_public"] != "shown" {
		t.Error("the unfiltered map is missing entries")
	}

	if err := c.SetSetting(ctx, "unit_private", "still-hidden", "public"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, ok := c.AllSettings("public")["unit_private"]; ok {
		t.Error("updating a private setting's value published it")
	}

	if err := c.SetSettingScope(ctx, "unit_private", "public"); err != nil {
		t.Fatalf("re-scope: %v", err)
	}
	if _, ok := c.AllSettings("public")["unit_private"]; !ok {
		t.Error("a setting promoted to public was still filtered out")
	}

	if err := c.SetSetting(ctx, "unit_defaulted", "value", ""); err != nil {
		t.Fatalf("default scope: %v", err)
	}
	if _, ok := c.AllSettings("public")["unit_defaulted"]; ok {
		t.Error("a setting written with no scope defaulted to public")
	}
}

func TestReloadPreservesScopes(t *testing.T) {

	env := testsupport.New(t)
	ctx := context.Background()

	if err := env.Core.ReloadSettings(ctx); err != nil {
		t.Fatalf("reload: %v", err)
	}
	pub := env.Core.AllSettings("public")
	if _, leaked := pub["webhook_secret"]; leaked {
		t.Error("webhook_secret became public after a settings reload")
	}
	if pub["site_name"] == "" {
		t.Error("a genuinely public setting went missing after a reload")
	}
}

func TestCacheStoresExpiresAndClears(t *testing.T) {
	env := testsupport.New(t)
	c := env.Core

	if _, ok := c.CacheGet("absent"); ok {
		t.Error("an unset key reported a hit")
	}

	c.CacheSet("alpha", "one", time.Minute)
	c.CacheSet("alpha:child", "two", time.Minute)
	c.CacheSet("beta", "three", time.Minute)

	if v, ok := c.CacheGet("alpha"); !ok || v != "one" {
		t.Errorf("CacheGet(alpha) = %v, %v", v, ok)
	}
	if c.CacheSize() < 3 {
		t.Errorf("CacheSize = %d, want at least 3", c.CacheSize())
	}

	if n := c.CacheClear("alpha"); n != 2 {
		t.Errorf("CacheClear(alpha) removed %d entries, want 2", n)
	}
	if _, ok := c.CacheGet("alpha"); ok {
		t.Error("alpha survived the prefix clear")
	}
	if _, ok := c.CacheGet("beta"); !ok {
		t.Error("beta should not have been cleared")
	}

	c.CacheSet("gamma", "four", time.Minute)
	if n := c.CacheClear(""); n < 2 {
		t.Errorf("CacheClear(\"\") removed %d entries, want all of them", n)
	}
	if c.CacheSize() != 0 {
		t.Errorf("CacheSize after a full clear = %d", c.CacheSize())
	}
}

func TestCacheEntriesExpire(t *testing.T) {
	env := testsupport.New(t)
	c := env.Core

	c.CacheSet("short", "value", 20*time.Millisecond)
	if _, ok := c.CacheGet("short"); !ok {
		t.Fatal("entry missing immediately after being set")
	}
	time.Sleep(40 * time.Millisecond)
	if _, ok := c.CacheGet("short"); ok {
		t.Error("an expired entry was still served")
	}
}

func TestAuditWritesAnAttributableRow(t *testing.T) {
	env := testsupport.New(t)
	ctx := context.Background()

	env.Core.Audit(ctx, 7, "customer", "unit.action", "orders", "123", "10.0.0.1",
		map[string]any{"note": "hello"})

	row, err := env.DB.MapOne(ctx,
		`SELECT actor_id,actor_role,action,entity,entity_id,ip,meta_json
		 FROM audit_log WHERE action='unit.action' ORDER BY id DESC LIMIT 1`)
	if err != nil {
		t.Fatalf("audit row not found: %v", err)
	}
	if got := toS(row["actor_role"]); got != "customer" {
		t.Errorf("actor_role = %q", got)
	}
	if got := toS(row["entity"]); got != "orders" {
		t.Errorf("entity = %q", got)
	}
	if got := toS(row["ip"]); got != "10.0.0.1" {
		t.Errorf("ip = %q", got)
	}
	if meta := toS(row["meta_json"]); !strings.Contains(meta, "hello") {
		t.Errorf("meta_json = %q, want the supplied detail", meta)
	}

	env.Core.Audit(ctx, 0, "system", "unit.system", "cron_jobs", "x", "", nil)
	actor, err := env.DB.Scalar(ctx, `SELECT actor_id FROM audit_log WHERE action='unit.system'`)
	if err != nil {
		t.Fatalf("system audit row: %v", err)
	}
	if actor.Valid {
		t.Errorf("system action recorded actor_id=%q, want NULL", actor.String)
	}
}

func TestLogErrorAndNotifyPersist(t *testing.T) {
	env := testsupport.New(t)
	ctx := context.Background()

	env.Core.LogError(ctx, "error", "UNIT_CODE", "something broke", "/unit/path", "stack here")
	n, err := env.DB.Scalar(ctx, `SELECT COUNT(*) FROM error_log WHERE code='UNIT_CODE'`)
	if err != nil || n.String != "1" {
		t.Errorf("error log rows = %v (%v)", n.String, err)
	}

	env.Core.Notify(ctx, 7, "system", "Unit title", "Unit body", "/unit")
	n, err = env.DB.Scalar(ctx,
		`SELECT COUNT(*) FROM notifications WHERE user_id=7 AND title='Unit title'`)
	if err != nil || n.String != "1" {
		t.Errorf("notification rows = %v (%v)", n.String, err)
	}
}

func TestUptimeAdvances(t *testing.T) {
	env := testsupport.New(t)
	first := env.Core.Uptime()
	time.Sleep(10 * time.Millisecond)
	if second := env.Core.Uptime(); second <= first {
		t.Errorf("uptime did not advance: %v then %v", first, second)
	}
}

func toS(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
