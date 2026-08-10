package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

func HashPassword(password string) ([]byte, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(append(salt, []byte(password)...))
	return []byte("sha256$" + base64.RawURLEncoding.EncodeToString(salt) + "$" + base64.RawURLEncoding.EncodeToString(sum[:])), nil
}

func CheckPassword(encoded []byte, password string) bool {
	parts := strings.Split(string(encoded), "$")
	if len(parts) != 3 || parts[0] != "sha256" {
		return false
	}
	salt, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	want, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	sum := sha256.Sum256(append(salt, []byte(password)...))
	return hmac.Equal(sum[:], want)
}

type Claims struct {
	UserID int64  `json:"uid"`
	Email  string `json:"email"`
	Exp    int64  `json:"exp"`
}

func SignJWT(secret string, c Claims) (string, error) {
	head := b64(`{"alg":"HS256","typ":"JWT"}`)
	bodyBytes, _ := json.Marshal(c)
	body := base64.RawURLEncoding.EncodeToString(bodyBytes)
	sig := sign(secret, head+"."+body)
	return head + "." + body + "." + sig, nil
}

func ParseJWT(secret, token string) (Claims, error) {
	var c Claims
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return c, errors.New("bad token")
	}
	if !hmac.Equal([]byte(sign(secret, parts[0]+"."+parts[1])), []byte(parts[2])) {
		return c, errors.New("bad signature")
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(body, &c); err != nil {
		return c, err
	}
	if time.Now().Unix() > c.Exp {
		return c, errors.New("expired token")
	}
	return c, nil
}

func NewToken(n int) (string, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b), err
}
func SHA256Hex(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func NewAPIKey() (string, string, error) {
	raw, err := NewToken(32)
	if err != nil {
		return "", "", err
	}
	key := "sq_live_" + raw
	return key, key[:16], nil
}
func b64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
func sign(secret, data string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(data))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func TokenTTL() int64        { return time.Now().Add(24 * time.Hour).Unix() }
func ResetExpiry() time.Time { return time.Now().Add(30 * time.Minute) }
