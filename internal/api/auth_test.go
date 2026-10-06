package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/arena/avaluna/internal/auth"
)

func TestRepeatedBadPasswordsLockTheAccount(t *testing.T) {
	env := newEnv(t)
	const email = "zahra@example.com"

	for i := 1; i <= 5; i++ {
		rec, _ := call(t, env, http.MethodPost, "/api/v1/auth/login",
			`{"email":"`+email+`","password":"DefinitelyWrong`+fmt.Sprint(i)+`"}`, "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d, want 401", i, rec.Code)
		}
	}
	if n := atoiOrZero(scalar(t, env, `SELECT failed_logins FROM users WHERE email=?`, email)); n < 5 {
		t.Errorf("failed_logins = %d, want at least 5", n)
	}
	if lock := scalar(t, env, `SELECT COALESCE(locked_until,'') FROM users WHERE email=?`, email); lock == "" {
		t.Fatal("the account was not locked after five failures")
	}

	rec, body := call(t, env, http.MethodPost, "/api/v1/auth/login",
		`{"email":"`+email+`","password":"Customer@12345"}`, "")
	if rec.Code != http.StatusForbidden {
		t.Errorf("login during a lock = %d, want 403", rec.Code)
	}
	if !strings.Contains(strings.ToLower(body.Error.Message), "lock") {
		t.Errorf("message = %q, want it to explain the lock", body.Error.Message)
	}

	if n := atoiOrZero(scalar(t, env,
		`SELECT COUNT(*) FROM audit_log WHERE action='auth.login_failed'`)); n < 5 {
		t.Errorf("audited failures = %d, want at least 5", n)
	}
}

func TestSuccessfulLoginClearsTheFailureCounter(t *testing.T) {
	env := newEnv(t)
	const email = "reza@example.com"

	for i := 0; i < 3; i++ {
		call(t, env, http.MethodPost, "/api/v1/auth/login",
			`{"email":"`+email+`","password":"Nope"}`, "")
	}
	if n := atoiOrZero(scalar(t, env, `SELECT failed_logins FROM users WHERE email=?`, email)); n != 3 {
		t.Fatalf("failed_logins = %d, want 3", n)
	}

	rec, _ := call(t, env, http.MethodPost, "/api/v1/auth/login",
		`{"email":"`+email+`","password":"Customer@12345"}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("login = %d, want 200", rec.Code)
	}
	if n := atoiOrZero(scalar(t, env, `SELECT failed_logins FROM users WHERE email=?`, email)); n != 0 {
		t.Errorf("failed_logins = %d after a good login, want 0", n)
	}
	if last := scalar(t, env, `SELECT COALESCE(last_login_at,'') FROM users WHERE email=?`, email); last == "" {
		t.Error("last_login_at was not stamped")
	}
}

func TestBannedAccountCannotSignIn(t *testing.T) {
	env := newEnv(t)
	const email = "john@example.com"

	if _, err := env.DB.Write(context.Background(),
		`UPDATE users SET status='banned' WHERE email=?`, email); err != nil {
		t.Fatalf("ban: %v", err)
	}
	rec, body := call(t, env, http.MethodPost, "/api/v1/auth/login",
		`{"email":"`+email+`","password":"Customer@12345"}`, "")
	if rec.Code != http.StatusForbidden {
		t.Errorf("banned login = %d, want 403", rec.Code)
	}
	if !strings.Contains(strings.ToLower(body.Error.Message), "suspend") {
		t.Errorf("message = %q", body.Error.Message)
	}
}

func TestUnknownEmailAndWrongPasswordAreIndistinguishable(t *testing.T) {
	env := newEnv(t)

	unknown, uBody := call(t, env, http.MethodPost, "/api/v1/auth/login",
		`{"email":"nobody@example.com","password":"Customer@12345"}`, "")
	wrong, wBody := call(t, env, http.MethodPost, "/api/v1/auth/login",
		`{"email":"ali@example.com","password":"WrongPassword"}`, "")

	if unknown.Code != wrong.Code {
		t.Errorf("status differs: unknown=%d wrong=%d — that reveals which accounts exist",
			unknown.Code, wrong.Code)
	}
	if uBody.Error.Message != wBody.Error.Message {
		t.Errorf("message differs: %q vs %q", uBody.Error.Message, wBody.Error.Message)
	}
}

func TestTwoFactorGateBlocksUntilACodeIsSupplied(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	const email = "emma@example.com"

	secret := auth.NewTOTPSecret()
	if _, err := env.DB.Write(ctx,
		`UPDATE users SET twofa_enabled=1, twofa_secret=? WHERE email=?`, secret, email); err != nil {
		t.Fatalf("enable 2FA: %v", err)
	}

	rec, challenge := call(t, env, http.MethodPost, "/api/v1/auth/login",
		`{"email":"`+email+`","password":"Customer@12345"}`, "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("2FA challenge = %d, want 202", rec.Code)
	}
	var data map[string]any
	if err := json.Unmarshal(challenge.Data, &data); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if data["twoFactorRequired"] != true {
		t.Errorf("payload = %v, want twoFactorRequired", data)
	}
	if strings.Contains(string(challenge.Data), "accessToken") {
		t.Error("a token was issued before the second factor was proven")
	}

	bad, _ := call(t, env, http.MethodPost, "/api/v1/auth/login",
		`{"email":"`+email+`","password":"Customer@12345","code":"000000"}`, "")
	if bad.Code != http.StatusUnauthorized {
		t.Errorf("wrong 2FA code = %d, want 401", bad.Code)
	}

	good, ok := call(t, env, http.MethodPost, "/api/v1/auth/login",
		`{"email":"`+email+`","password":"Customer@12345","code":"`+auth.TOTPCode(secret, time.Now().UTC())+`"}`, "")
	if good.Code != http.StatusOK {
		t.Fatalf("valid 2FA login = %d: %s", good.Code, ok.Error.Message)
	}
	if !strings.Contains(string(ok.Data), "accessToken") {
		t.Error("no access token was issued after a valid code")
	}
}

func TestRefreshRotatesTheTokenAndDetectsReuse(t *testing.T) {
	env := newEnv(t)

	rec, _ := call(t, env, http.MethodPost, "/api/v1/auth/login",
		`{"email":"ali@example.com","password":"Customer@12345"}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("login = %d", rec.Code)
	}
	var first string
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == "ecms_refresh" {
			first = ck.Value
		}
	}
	if first == "" {
		t.Fatal("login did not set a refresh cookie")
	}

	r1 := env.Request(http.MethodPost, "/api/v1/auth/refresh-token",
		`{"refreshToken":"`+first+`"}`, nil)
	if r1.Code != http.StatusOK {
		t.Fatalf("refresh = %d: %s", r1.Code, r1.Body.String())
	}
	var second string
	for _, ck := range r1.Result().Cookies() {
		if ck.Name == "ecms_refresh" {
			second = ck.Value
		}
	}
	if second == "" || second == first {
		t.Fatal("the refresh token was not rotated")
	}
	if rev := scalar(t, env,
		`SELECT COALESCE(revoked_at,'') FROM refresh_tokens WHERE token_hash=?`,
		auth.HashToken(first)); rev == "" {
		t.Error("the spent refresh token was not revoked")
	}

	replay := env.Request(http.MethodPost, "/api/v1/auth/refresh-token",
		`{"refreshToken":"`+first+`"}`, nil)
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf("replay = %d, want 401", replay.Code)
	}
	if !strings.Contains(strings.ToLower(replay.Body.String()), "reuse") {
		t.Errorf("replay response did not report reuse: %s", replay.Body.String())
	}

	after := env.Request(http.MethodPost, "/api/v1/auth/refresh-token",
		`{"refreshToken":"`+second+`"}`, nil)
	if after.Code == http.StatusOK {
		t.Error("the whole token family should have been revoked after reuse")
	}
	if n := atoiOrZero(scalar(t, env,
		`SELECT COUNT(*) FROM audit_log WHERE action='auth.refresh_reuse_detected'`)); n == 0 {
		t.Error("the reuse was not audited")
	}
}

func TestRefreshRejectsMissingAndUnknownTokens(t *testing.T) {
	env := newEnv(t)

	none := env.Request(http.MethodPost, "/api/v1/auth/refresh-token", `{}`, nil)
	if none.Code != http.StatusUnauthorized {
		t.Errorf("no token = %d, want 401", none.Code)
	}
	bogus := env.Request(http.MethodPost, "/api/v1/auth/refresh-token",
		`{"refreshToken":"not-a-real-token"}`, nil)
	if bogus.Code != http.StatusUnauthorized {
		t.Errorf("unknown token = %d, want 401", bogus.Code)
	}
}

func TestLogoutRevokesTheRefreshToken(t *testing.T) {
	env := newEnv(t)

	rec, _ := call(t, env, http.MethodPost, "/api/v1/auth/login",
		`{"email":"zahra@example.com","password":"Customer@12345"}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("login = %d", rec.Code)
	}
	var refresh, access string
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == "ecms_refresh" {
			refresh = ck.Value
		}
	}
	var payload struct {
		Tokens struct {
			AccessToken string `json:"accessToken"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(dataOf(t, rec), &payload); err == nil {
		access = payload.Tokens.AccessToken
	}

	out := env.Request(http.MethodPost, "/api/v1/auth/logout", "",
		map[string]string{"Authorization": "Bearer " + access, "Cookie": "ecms_refresh=" + refresh})
	if out.Code != http.StatusOK && out.Code != http.StatusNoContent {
		t.Fatalf("logout = %d: %s", out.Code, out.Body.String())
	}

	after := env.Request(http.MethodPost, "/api/v1/auth/refresh-token",
		`{"refreshToken":"`+refresh+`"}`, nil)
	if after.Code == http.StatusOK {
		t.Error("the refresh token still worked after logging out")
	}
}

func TestRegistrationAcceptsAReferralCode(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()

	referrer := scalar(t, env, `SELECT referral_code FROM users WHERE email='ali@example.com'`)
	if referrer == "" {
		t.Skip("the seed user has no referral code")
	}

	rec, _ := call(t, env, http.MethodPost, "/api/v1/auth/register",
		`{"email":"referred@example.com","password":"StrongPass123",
		  "firstName":"Ref","lastName":"Erred","referralCode":"`+referrer+`"}`, "")
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("register = %d: %s", rec.Code, rec.Body.String())
	}

	n, err := env.DB.Count(ctx,
		`SELECT COUNT(*) FROM referrals r JOIN users u ON u.id=r.referred_user_id
		 WHERE u.email='referred@example.com'`)
	if err != nil {

		t.Logf("referral lookup: %v", err)
	} else if n == 0 {
		t.Error("the referral was not recorded")
	}

	if got := scalar(t, env, `SELECT role FROM users WHERE email='referred@example.com'`); got != "customer" {
		t.Errorf("role = %q, want customer", got)
	}
}

func TestRegistrationRejectsADuplicateEmail(t *testing.T) {
	env := newEnv(t)
	rec, body := call(t, env, http.MethodPost, "/api/v1/auth/register",
		`{"email":"ali@example.com","password":"StrongPass123","firstName":"Imp","lastName":"Ostor"}`, "")
	if rec.Code != http.StatusConflict && rec.Code != http.StatusBadRequest {
		t.Errorf("duplicate register = %d, want a rejection", rec.Code)
	}
	if body.Success {
		t.Error("a duplicate registration reported success")
	}

	if n := atoiOrZero(scalar(t, env, `SELECT COUNT(*) FROM users WHERE email='ali@example.com'`)); n != 1 {
		t.Errorf("there are now %d accounts for that address", n)
	}
}

func TestRegistrationRejectsAMalformedEmail(t *testing.T) {
	env := newEnv(t)
	for _, email := range []string{"", "not-an-email", "@example.com", "two@@example.com", "spaces in@example.com"} {
		rec, _ := call(t, env, http.MethodPost, "/api/v1/auth/register",
			`{"email":"`+email+`","password":"StrongPass123","firstName":"A","lastName":"B"}`, "")
		if rec.Code < 400 {
			t.Errorf("register with email %q = %d, want a rejection", email, rec.Code)
		}
	}
}
