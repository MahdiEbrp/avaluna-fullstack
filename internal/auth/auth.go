package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/argon2"
)

const (
	RoleGuest      = "guest"
	RoleCustomer   = "customer"
	RoleVendor     = "vendor"
	RoleEditor     = "editor"
	RoleSupport    = "support"
	RoleAdmin      = "admin"
	RoleSuperAdmin = "super_admin"
)

var rank = map[string]int{
	RoleGuest: 0, RoleCustomer: 1, RoleVendor: 2, RoleEditor: 3,
	RoleSupport: 4, RoleAdmin: 5, RoleSuperAdmin: 6,
}

func RoleAtLeast(role, min string) bool { return rank[role] >= rank[min] }

func IsStaff(role string) bool { return rank[role] >= rank[RoleEditor] }

type argonParams struct {
	time, memory uint32
	threads      uint8
	keyLen       uint32
}

var defaultArgon = argonParams{time: 2, memory: 64 * 1024, threads: 2, keyLen: 32}

var hashSem = make(chan struct{}, maxConcurrentHashes())

func maxConcurrentHashes() int {
	n := runtime.GOMAXPROCS(0)
	if n < 2 {
		n = 2
	}
	if n > 8 {
		n = 8
	}
	return n
}

func MaxConcurrentHashes() int { return cap(hashSem) }

func acquireHashSlot() { hashSem <- struct{}{} }
func releaseHashSlot() { <-hashSem }

func HashPassword(password string, pepper []byte) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	p := defaultArgon
	acquireHashSlot()
	key := argon2.IDKey(peppered(password, pepper), salt, p.time, p.memory, p.threads, p.keyLen)
	releaseHashSlot()
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		p.memory, p.time, p.threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

func VerifyPassword(password, encoded string, pepper []byte) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var mem, tim uint32
	var par uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &tim, &par); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	acquireHashSlot()
	got := argon2.IDKey(peppered(password, pepper), salt, tim, mem, par, uint32(len(want)))
	releaseHashSlot()
	return subtle.ConstantTimeCompare(got, want) == 1
}

func peppered(password string, pepper []byte) []byte {
	mac := hmac.New(sha256.New, pepper)
	mac.Write([]byte(password))
	return mac.Sum(nil)
}

type Claims struct {
	UserID int    `json:"uid"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	Locale string `json:"locale"`
	jwt.RegisteredClaims
}

var ErrInvalidToken = errors.New("invalid or expired token")

func Issue(secret []byte, userID int, email, role, locale string, ttl time.Duration) (string, error) {
	now := time.Now().UTC()
	c := Claims{
		UserID: userID, Email: email, Role: role, Locale: locale,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.Itoa(userID),
			Issuer:    "avaluna",
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			ID:        RandomToken(8),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(secret)
}

func Parse(secret []byte, tokenStr string) (*Claims, error) {
	tok, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return secret, nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || !tok.Valid {
		return nil, ErrInvalidToken
	}
	c, ok := tok.Claims.(*Claims)
	if !ok {
		return nil, ErrInvalidToken
	}
	return c, nil
}

func RandomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte(time.Now().String()))[:n*2]
	}
	return hex.EncodeToString(b)
}

func HashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

func NewTOTPSecret() string {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		copy(b, []byte(time.Now().String()))
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
}

func TOTPCode(secret string, t time.Time) string {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil {
		return ""
	}
	counter := uint64(t.UTC().Unix()) / 30
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(buf)
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	code := (binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff) % 1000000
	return fmt.Sprintf("%06d", code)
}

func VerifyTOTP(secret, code string) bool {
	now := time.Now().UTC()
	for _, skew := range []time.Duration{-30 * time.Second, 0, 30 * time.Second} {
		if subtle.ConstantTimeCompare([]byte(TOTPCode(secret, now.Add(skew))), []byte(code)) == 1 {
			return true
		}
	}
	return false
}

func SignHMAC(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func VerifyHMAC(secret string, payload []byte, signature string) bool {
	return hmac.Equal([]byte(SignHMAC(secret, payload)), []byte(signature))
}
