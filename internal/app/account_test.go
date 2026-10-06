package app_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/arena/avaluna/internal/testsupport"
)

func TestRegisterThroughTheHTMLForm(t *testing.T) {
	env := testsupport.New(t)
	b := env.Browser()

	if rec := b.Get("/register"); rec.Code != http.StatusOK {
		t.Fatalf("GET /register = %d", rec.Code)
	}

	form := url.Values{
		"firstName": {"Html"}, "lastName": {"Shopper"},
		"email": {"html.shopper@example.com"}, "password": {"StrongPass123"},
	}
	rec := b.PostForm("/register", form)
	if rec.Code >= 500 {
		t.Fatalf("register = %d: %s", rec.Code, rec.Body.String())
	}

	if rec.Code == http.StatusSeeOther {
		if acct := b.Get("/account"); acct.Code != http.StatusOK {
			t.Errorf("GET /account after registering = %d, want a signed-in page", acct.Code)
		}
	}

	again := b.PostForm("/register", form)
	if again.Code >= 500 {
		t.Errorf("duplicate registration = %d", again.Code)
	}
}

func TestRegisterFormRejectsWeakInput(t *testing.T) {
	env := testsupport.New(t)
	b := env.Browser()

	for _, tc := range []struct {
		name string
		form url.Values
	}{
		{"missing email", url.Values{"firstName": {"A"}, "password": {"StrongPass123"}}},
		{"short password", url.Values{"email": {"weak@example.com"}, "password": {"abc"}}},
		{"empty password", url.Values{"email": {"weak2@example.com"}, "password": {""}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := b.PostForm("/register", tc.form)
			if rec.Code >= 500 {
				t.Errorf("= %d, a rejected registration must not 500", rec.Code)
			}
			if rec.Code == http.StatusSeeOther {
				t.Error("an invalid registration was accepted")
			}
		})
	}
}

func TestLoginLogoutRoundTrip(t *testing.T) {
	env := testsupport.New(t)
	b := env.Browser()

	rec := b.PostForm("/login", url.Values{
		"email": {"ali@example.com"}, "password": {"Customer@12345"},
	})
	if rec.Code >= 500 {
		t.Fatalf("login = %d", rec.Code)
	}
	if acct := b.Get("/account"); acct.Code != http.StatusOK {
		t.Fatalf("GET /account while signed in = %d", acct.Code)
	}

	out := b.Get("/logout")
	if out.Code >= 500 {
		t.Fatalf("logout = %d", out.Code)
	}

	after := b.Get("/account")
	if after.Code == http.StatusOK {
		t.Error("the account page still rendered after logging out")
	}
}

func TestLoginFormRejectsBadCredentials(t *testing.T) {
	env := testsupport.New(t)
	b := env.Browser()

	for _, tc := range []struct{ name, email, pass string }{
		{"wrong password", "ali@example.com", "WrongPassword1"},
		{"unknown user", "nobody@example.com", "Customer@12345"},
		{"empty password", "ali@example.com", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := b.PostForm("/login", url.Values{"email": {tc.email}, "password": {tc.pass}})
			if rec.Code >= 500 {
				t.Errorf("= %d, a failed login must not 500", rec.Code)
			}
			if rec.Code == http.StatusSeeOther {
				t.Error("a bad credential was accepted")
			}
		})
	}

	if rec := b.Get("/account"); rec.Code == http.StatusOK {
		t.Error("failed logins left a usable session")
	}
}

func TestForgotPagePointsAtTheResetAPI(t *testing.T) {
	env := testsupport.New(t)
	b := env.Browser()

	rec := b.Get("/forgot")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /forgot = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "/auth/forgot-password") {
		t.Error("the page does not tell the visitor how to reset a password")
	}
}

func TestAccountTabsRequireSignIn(t *testing.T) {
	env := testsupport.New(t)
	anon := env.Browser()

	tabs := []string{
		"/account", "/account/orders", "/account/wallet", "/account/loyalty",
		"/account/wishlist", "/account/addresses", "/account/returns",
		"/account/subscriptions", "/account/giftcards", "/account/referral",
		"/account/notifications",
	}
	for _, tab := range tabs {
		if rec := anon.Get(tab); rec.Code == http.StatusOK {
			t.Errorf("GET %s rendered for a signed-out visitor", tab)
		}
	}

	b := env.Browser()
	b.PostForm("/login", url.Values{"email": {"ali@example.com"}, "password": {"Customer@12345"}})
	for _, tab := range tabs {
		rec := b.Get(tab)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s while signed in = %d", tab, rec.Code)
		}
		if rec.Body.Len() == 0 {
			t.Errorf("GET %s rendered an empty body", tab)
		}
	}
}

func TestSignedInHeaderShowsTheAccount(t *testing.T) {
	env := testsupport.New(t)
	b := env.Browser()

	anonHome := env.Browser().Get("/").Body.String()
	if !strings.Contains(anonHome, "/login") {
		t.Error("a signed-out header should offer a login link")
	}

	b.PostForm("/login", url.Values{"email": {"ali@example.com"}, "password": {"Customer@12345"}})
	home := b.Get("/").Body.String()
	if !strings.Contains(home, "/account") {
		t.Error("a signed-in header should link to the account area")
	}
}
