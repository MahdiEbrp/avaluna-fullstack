package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/arena/avaluna/internal/auth"
	"github.com/arena/avaluna/internal/core"
	"github.com/arena/avaluna/internal/httpx"
	"github.com/arena/avaluna/internal/middleware"
)

var webhookClient = &http.Client{Timeout: 8 * time.Second}

func registerWebhookModule(rt *Router, c *core.Core) {
	rt.Module("20-integrations")

	rt.Handle(http.MethodGet, "/integrations/webhooks", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		rows, _ := c.DB.Maps(r.Context(),
			`SELECT id,name,url,events,active,failures,last_status,created_at FROM webhooks ORDER BY id DESC`)
		writeOK(w, rows)
	})

	rt.Handle(http.MethodPost, "/integrations/webhooks", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		m, _ := body(r)
		url := sVal(m, "url")
		if url == "" || !strings.HasPrefix(url, "http") {
			httpx.Fail(w, httpx.CodeValidation, "", httpx.FieldIssue{Field: "url", Issue: "must be an absolute http(s) URL"})
			return
		}
		secret := sVal(m, "secret")
		if secret == "" {
			secret = auth.RandomToken(16)
		}
		events := sVal(m, "events")
		if events == "" {
			events = "order.created"
		}
		res, err := c.DB.Write(r.Context(), `INSERT INTO webhooks(name,url,events,secret,active) VALUES(?,?,?,?,1)`,
			sVal(m, "name"), url, events, secret)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		id, _ := res.LastInsertId()
		audit(c, r, "webhook.create", "webhooks", toString(id), map[string]any{"url": url})
		writeCreated(w, map[string]any{"id": id, "secret": secret, "events": events,
			"signatureHeader": "X-Signature (HMAC-SHA256 hex of the raw body)"})
	})

	rt.Handle(http.MethodDelete, "/integrations/webhooks/{id}", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		id, _ := httpx.PathInt(r, "id")
		_, _ = c.DB.Write(r.Context(), `DELETE FROM webhooks WHERE id=?`, id)
		audit(c, r, "webhook.delete", "webhooks", toString(id), nil)
		httpx.NoBody(w, "Webhook deleted")
	})

	rt.Handle(http.MethodGet, "/integrations/webhooks/{id}/deliveries", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		id, _ := httpx.PathInt(r, "id")
		rows, _ := c.DB.Maps(r.Context(),
			`SELECT * FROM webhook_deliveries WHERE webhook_id=? ORDER BY id DESC LIMIT 100`, id)
		writeOK(w, rows)
	})

	rt.Handle(http.MethodPost, "/integrations/webhooks/{id}/test", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		id, _ := httpx.PathInt(r, "id")
		row, err := c.DB.MapOne(r.Context(), `SELECT * FROM webhooks WHERE id=?`, id)
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		status, errMsg := deliverWebhook(r.Context(), c, row, "ping",
			map[string]any{"ping": true, "at": time.Now().UTC().Format(time.RFC3339)})
		writeOK(w, map[string]any{"status": status, "error": errMsg})
	})

	rt.Handle(http.MethodGet, "/integrations/api-keys", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		rows, _ := c.DB.Maps(r.Context(),
			`SELECT id,name,prefix,scopes,owner_id,last_used_at,revoked_at,created_at FROM api_keys ORDER BY id DESC`)
		writeOK(w, rows)
	})

	rt.Handle(http.MethodPost, "/integrations/api-keys", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		m, _ := body(r)
		name := sVal(m, "name")
		if name == "" {
			httpx.Fail(w, httpx.CodeValidation, "name is required")
			return
		}
		scopes := sVal(m, "scopes")
		if scopes == "" {
			scopes = "read"
		}
		prefix := "ak_" + auth.RandomToken(4)
		raw := prefix + "." + auth.RandomToken(24)
		res, err := c.DB.Write(r.Context(),
			`INSERT INTO api_keys(name,prefix,key_hash,scopes,owner_id) VALUES(?,?,?,?,?)`,
			name, prefix, auth.HashToken(raw), scopes, nullIf(middleware.UserID(r.Context())))
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		id, _ := res.LastInsertId()
		audit(c, r, "apikey.create", "api_keys", toString(id), map[string]any{"name": name, "scopes": scopes})
		writeCreated(w, map[string]any{"id": id, "apiKey": raw, "prefix": prefix, "scopes": scopes,
			"warning": "This key is shown only once — store it securely."})
	})

	rt.Handle(http.MethodDelete, "/integrations/api-keys/{id}", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		id, _ := httpx.PathInt(r, "id")
		_, _ = c.DB.Write(r.Context(), `UPDATE api_keys SET revoked_at=datetime('now') WHERE id=?`, id)
		audit(c, r, "apikey.revoke", "api_keys", toString(id), nil)
		httpx.NoBody(w, "API key revoked")
	})
}

func DispatchWebhook(ctx context.Context, c *core.Core, event string, payload any) {
	rows, err := c.DB.Maps(ctx, `SELECT * FROM webhooks WHERE active=1`)
	if err != nil {
		return
	}
	for _, hook := range rows {
		events := asStr(hook["events"])
		if events != "*" && !strings.Contains(events, event) {
			continue
		}
		h := hook
		if c.Queue != nil {
			_, _ = c.Queue.Enqueue(ctx, "webhook", "webhook.deliver", map[string]any{
				"webhookId": h["id"], "event": event, "payload": payload,
			}, 5)
			continue
		}
		go func() {
			bg, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_, _ = deliverWebhook(bg, c, h, event, payload)
		}()
	}
}

func deliverWebhook(ctx context.Context, c *core.Core, hook map[string]any, event string, payload any) (int, string) {
	raw := []byte(jsonStr(map[string]any{
		"event": event, "data": payload, "at": time.Now().UTC().Format(time.RFC3339),
	}))
	sig := auth.SignHMAC(asStr(hook["secret"]), raw)
	res, err := c.DB.Write(ctx,
		`INSERT INTO webhook_deliveries(webhook_id,event,payload,signature,status,attempts) VALUES(?,?,?,?,'pending',1)`,
		hook["id"], event, string(raw), sig)
	var deliveryID int64
	if err == nil {
		deliveryID, _ = res.LastInsertId()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, asStr(hook["url"]), bytes.NewReader(raw))
	if err != nil {
		markDelivery(ctx, c, deliveryID, hook, 0, "failed", err.Error())
		return 0, err.Error()
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Signature", sig)
	req.Header.Set("X-Event", event)
	req.Header.Set("User-Agent", "Avaluna-Webhook/1.0")

	resp, err := webhookClient.Do(req)
	if err != nil {
		markDelivery(ctx, c, deliveryID, hook, 0, "failed", err.Error())
		return 0, err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	status := "delivered"
	errMsg := ""
	if resp.StatusCode >= 300 {
		status = "failed"
		errMsg = "non-2xx response"
	}
	markDelivery(ctx, c, deliveryID, hook, resp.StatusCode, status, errMsg)
	return resp.StatusCode, errMsg
}

func markDelivery(ctx context.Context, c *core.Core, id int64, hook map[string]any, code int, status, errMsg string) {
	if id > 0 {
		_, _ = c.DB.Write(ctx, `UPDATE webhook_deliveries SET status=?, response_code=? WHERE id=?`, status, code, id)
	}
	if status == "delivered" {
		_, _ = c.DB.Write(ctx, `UPDATE webhooks SET failures=0, last_status=? WHERE id=?`, code, hook["id"])
		return
	}
	_, _ = c.DB.Write(ctx, `UPDATE webhooks SET failures=failures+1, last_status=? WHERE id=?`, code, hook["id"])
	c.Log.Warn("webhook delivery failed", "url", hook["url"], "error", errMsg)
}

func RetryFailedWebhooks(ctx context.Context, c *core.Core, limit int) int {
	rows, err := c.DB.Maps(ctx, `
		SELECT d.id AS delivery_id, d.event, d.payload, w.* FROM webhook_deliveries d
		JOIN webhooks w ON w.id = d.webhook_id
		WHERE d.status='failed' AND d.attempts < 5 AND w.active=1
		ORDER BY d.id LIMIT ?`, limit)
	if err != nil {
		return 0
	}
	n := 0
	for _, row := range rows {
		_, _ = c.DB.Write(ctx, `UPDATE webhook_deliveries SET attempts=attempts+1 WHERE id=?`, row["delivery_id"])
		if code, _ := deliverWebhook(ctx, c, row, asStr(row["event"]), row["payload"]); code >= 200 && code < 300 {
			n++
		}
	}
	return n
}
