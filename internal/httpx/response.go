package httpx

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Pagination struct {
	Page       int    `json:"page"`
	Limit      int    `json:"limit"`
	TotalItems int    `json:"totalItems"`
	TotalPages int    `json:"totalPages"`
	NextCursor string `json:"nextCursor,omitempty"`
}

type Meta struct {
	Pagination *Pagination    `json:"pagination,omitempty"`
	Extra      map[string]any `json:"extra,omitempty"`
}

type Envelope struct {
	Success    bool   `json:"success"`
	StatusCode int    `json:"statusCode"`
	Message    string `json:"message,omitempty"`
	Data       any    `json:"data,omitempty"`
	Meta       *Meta  `json:"meta,omitempty"`
	Timestamp  string `json:"timestamp"`
}

type FieldIssue struct {
	Field string `json:"field"`
	Issue string `json:"issue"`
}

type ErrorBody struct {
	Code    string       `json:"code"`
	Message string       `json:"message"`
	Details []FieldIssue `json:"details,omitempty"`
}

type ErrorEnvelope struct {
	Success    bool      `json:"success"`
	StatusCode int       `json:"statusCode"`
	Error      ErrorBody `json:"error"`
	Timestamp  string    `json:"timestamp"`
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func OK(w http.ResponseWriter, data any) { Success(w, http.StatusOK, "OK", data, nil) }

func Created(w http.ResponseWriter, data any) {
	Success(w, http.StatusCreated, "Created", data, nil)
}

func NoBody(w http.ResponseWriter, msg string) {
	Success(w, http.StatusOK, msg, map[string]any{}, nil)
}

func Success(w http.ResponseWriter, status int, msg string, data any, meta *Meta) {
	JSON(w, status, Envelope{
		Success:    true,
		StatusCode: status,
		Message:    msg,
		Data:       data,
		Meta:       meta,
		Timestamp:  now(),
	})
}

func List(w http.ResponseWriter, items any, p Pagination) {
	Success(w, http.StatusOK, "OK", items, &Meta{Pagination: &p})
}

func Fail(w http.ResponseWriter, code, msg string, details ...FieldIssue) {
	status := StatusFor(code)
	if msg == "" {
		msg = DefaultMessage(code)
	}
	JSON(w, status, ErrorEnvelope{
		Success:    false,
		StatusCode: status,
		Error:      ErrorBody{Code: code, Message: msg, Details: details},
		Timestamp:  now(),
	})
}

func FailStatus(w http.ResponseWriter, status int, code, msg string, details ...FieldIssue) {
	JSON(w, status, ErrorEnvelope{
		Success:    false,
		StatusCode: status,
		Error:      ErrorBody{Code: code, Message: msg, Details: details},
		Timestamp:  now(),
	})
}

const (
	CodeValidation       = "VALIDATION_ERROR"
	CodeUnauthorized     = "UNAUTHORIZED"
	CodeForbidden        = "FORBIDDEN"
	CodeNotFound         = "NOT_FOUND"
	CodeConflict         = "CONFLICT"
	CodeOutOfStock       = "OUT_OF_STOCK"
	CodeCouponInvalid    = "COUPON_INVALID"
	CodePaymentFailed    = "PAYMENT_FAILED"
	CodeShippingUnavail  = "SHIPPING_UNAVAILABLE"
	CodeRateLimit        = "RATE_LIMIT_EXCEEDED"
	CodeIdempotency      = "IDEMPOTENCY_CONFLICT"
	CodeCronNotFound     = "CRON_JOB_NOT_FOUND"
	CodeCronRunning      = "CRON_JOB_ALREADY_RUNNING"
	CodeBackupInProgress = "BACKUP_IN_PROGRESS"
	CodeImportFailed     = "IMPORT_VALIDATION_FAILED"
	CodeSubPaymentFailed = "SUBSCRIPTION_PAYMENT_FAILED"
	CodeGiftRedeemed     = "GIFT_CARD_ALREADY_REDEEMED"
	CodeFlagDisabled     = "FEATURE_FLAG_DISABLED"
	CodeMaintenance      = "MAINTENANCE_MODE"
	CodeInternal         = "INTERNAL_SERVER_ERROR"
)

type codeSpec struct {
	status int
	msg    string
}

var registry = map[string]codeSpec{
	CodeValidation:       {400, "Validation failed"},
	CodeUnauthorized:     {401, "Authentication required"},
	CodeForbidden:        {403, "You do not have access to this resource"},
	CodeNotFound:         {404, "Resource not found"},
	CodeConflict:         {409, "Data conflict"},
	CodeOutOfStock:       {422, "Requested quantity not available"},
	CodeCouponInvalid:    {422, "Coupon is invalid or expired"},
	CodePaymentFailed:    {402, "Payment failed"},
	CodeShippingUnavail:  {422, "Shipping is not available for this destination"},
	CodeRateLimit:        {429, "Too many requests"},
	CodeIdempotency:      {409, "Duplicate financial request"},
	CodeCronNotFound:     {404, "Cron job not found"},
	CodeCronRunning:      {409, "Cron job is already running"},
	CodeBackupInProgress: {409, "Another backup is already running"},
	CodeImportFailed:     {422, "Import file structure is invalid"},
	CodeSubPaymentFailed: {402, "Recurring payment failed"},
	CodeGiftRedeemed:     {409, "Gift card already redeemed"},
	CodeFlagDisabled:     {403, "Feature is not enabled for this user"},
	CodeMaintenance:      {503, "Store is in maintenance mode"},
	CodeInternal:         {500, "Internal server error"},
}

func StatusFor(code string) int {
	if s, ok := registry[code]; ok {
		return s.status
	}
	return http.StatusInternalServerError
}

func DefaultMessage(code string) string {
	if s, ok := registry[code]; ok {
		return s.msg
	}
	return "Unexpected error"
}

func Codes() []map[string]any {
	out := make([]map[string]any, 0, len(registry))
	for c, s := range registry {
		out = append(out, map[string]any{"code": c, "status": s.status, "message": s.msg})
	}
	return out
}

func Decode(r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 8<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}

func DecodeLoose(r *http.Request, dst *map[string]any) error {
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") {
		r.Body = http.MaxBytesReader(nil, r.Body, 8<<20)
		m := map[string]any{}
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			return fmt.Errorf("invalid JSON body: %w", err)
		}
		*dst = m
		return nil
	}
	if err := r.ParseForm(); err != nil {
		return err
	}
	m := map[string]any{}
	for k, v := range r.Form {
		if len(v) == 1 {
			m[k] = v[0]
		} else {
			m[k] = v
		}
	}
	*dst = m
	return nil
}

func QueryInt(r *http.Request, key string, def, min, max int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

func PathInt(r *http.Request, key string) (int, bool) {
	n, err := strconv.Atoi(r.PathValue(key))
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

func PageParams(r *http.Request) (page, limit, offset int) {
	page = QueryInt(r, "page", 1, 1, 100000)
	limit = QueryInt(r, "limit", 20, 1, 200)
	return page, limit, (page - 1) * limit
}

func MakePagination(page, limit, total int) Pagination {
	pages := 0
	if limit > 0 {
		pages = (total + limit - 1) / limit
	}
	return Pagination{Page: page, Limit: limit, TotalItems: total, TotalPages: pages}
}
