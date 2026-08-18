package server

// This file contains a minimal, dependency-free Web Push implementation
// using only the Go standard library + golang.org/x/crypto (already a dependency).
//
// The Web Push protocol (RFC 8291 + VAPID RFC 8772) requires:
// 1. ECDH key agreement (P-256) to derive a shared key
// 2. AES-128-GCM encryption of the payload (aes128gcm content encoding)
// 3. VAPID JWT signing with ECDSA P-256

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ─── VAPID JWT ───────────────────────────────────────────

// vapidPrivateKeyToECDSA converts a base64url-encoded VAPID private key to an ECDSA P-256 key.
// The private key is the raw 32-byte scalar.
func vapidPrivateKeyToECDSA(privateKeyB64 string) (*ecdsa.PrivateKey, error) {
	raw, err := base64.RawURLEncoding.DecodeString(privateKeyB64)
	if err != nil {
		// Try standard URL encoding
		raw, err = base64.URLEncoding.DecodeString(privateKeyB64)
		if err != nil {
			return nil, fmt.Errorf("decode private key: %w", err)
		}
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("private key must be 32 bytes, got %d", len(raw))
	}

	curve := elliptic.P256()
	d := new(big.Int).SetBytes(raw)
	priv := &ecdsa.PrivateKey{
		PublicKey: ecdsa.PublicKey{
			Curve: curve,
		},
		D: d,
	}
	// Compute public key
	x, y := curve.ScalarBaseMult(raw)
	priv.PublicKey.X = x
	priv.PublicKey.Y = y
	return priv, nil
}

// vapidPublicKeyToUncompressed converts the ECDSA P-256 public key to the uncompressed point format.
// Returns 65 bytes: 0x04 || X(32) || Y(32)
func vapidPublicKeyToUncompressed(pub *ecdsa.PublicKey) []byte {
	out := make([]byte, 65)
	out[0] = 4
	xb := pub.X.FillBytes(make([]byte, 32))
	yb := pub.Y.FillBytes(make([]byte, 32))
	copy(out[1:33], xb)
	copy(out[33:65], yb)
	return out
}

// createVAPIDJWT creates a signed VAPID JWT for the given endpoint origin.
// Returns the JWT string and the base64url-encoded public key.
func createVAPIDJWT(priv *ecdsa.PrivateKey, origin, subject string) (string, string, error) {
	// JWT header
	header := map[string]string{
		"typ": "JWT",
		"alg": "ES256",
	}
	headerJSON, _ := json.Marshal(header)
	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)

	// JWT payload
	now := time.Now()
	payload := map[string]interface{}{
		"aud": origin,
		"sub": subject,
		"exp": now.Add(12 * time.Hour).Unix(),
	}
	payloadJSON, _ := json.Marshal(payload)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)

	signingInput := headerB64 + "." + payloadB64
	hash := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, priv, hash[:])
	if err != nil {
		return "", "", fmt.Errorf("sign JWT: %w", err)
	}

	// ES256 signature: r(32) || s(32) = 64 bytes
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	sigB64 := base64.RawURLEncoding.EncodeToString(sig)

	jwt := signingInput + "." + sigB64

	// Public key in base64url (uncompressed format)
	pubKey := vapidPublicKeyToUncompressed(&priv.PublicKey)
	pubKeyB64 := base64.RawURLEncoding.EncodeToString(pubKey)

	return jwt, pubKeyB64, nil
}

// ─── Web Push Encryption (RFC 8291 aes128gcm) ───────────

// encryptPayload encrypts the payload using aes128gcm content encoding (RFC 8188 + 8291).
func encryptPayload(payload []byte, p256dhB64, authB64 string, senderPriv *ecdsa.PrivateKey) ([]byte, error) {
	// 1. Decode the user agent public key (P-256, uncompressed: 65 bytes starting with 0x04)
	userPubKeyRaw, err := base64.RawURLEncoding.DecodeString(p256dhB64)
	if err != nil {
		userPubKeyRaw, err = base64.URLEncoding.DecodeString(p256dhB64)
		if err != nil {
			return nil, fmt.Errorf("decode p256dh: %w", err)
		}
	}
	if len(userPubKeyRaw) != 65 || userPubKeyRaw[0] != 4 {
		return nil, fmt.Errorf("invalid user public key format: len=%d, first byte=%d", len(userPubKeyRaw), userPubKeyRaw[0])
	}
	ux := new(big.Int).SetBytes(userPubKeyRaw[1:33])
	uy := new(big.Int).SetBytes(userPubKeyRaw[33:65])
	userPub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: ux, Y: uy}

	// 2. Generate ephemeral ECDH key pair (P-256)
	ephemD, _, _, err := elliptic.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ephemeral key: %w", err)
	}
	ephemPriv := &ecdsa.PrivateKey{
		PublicKey: ecdsa.PublicKey{Curve: elliptic.P256()},
		D:         new(big.Int).SetBytes(ephemD),
	}
	ex, ey := elliptic.P256().ScalarBaseMult(ephemD)
	ephemPriv.PublicKey.X = ex
	ephemPriv.PublicKey.Y = ey
	ephemPubUncompressed := vapidPublicKeyToUncompressed(&ephemPriv.PublicKey)

	// 3. ECDH shared secret: scalarMult(userPub, ephemPriv.D)
	sx, _ := userPub.ScalarMult(ux, uy, ephemD)
	if sx == nil {
		return nil, fmt.Errorf("ECDH shared secret computation failed")
	}
	sharedSecret := sx.FillBytes(make([]byte, 32))

	// 4. Decode auth secret
	authSecret, err := base64.RawURLEncoding.DecodeString(authB64)
	if err != nil {
		authSecret, err = base64.URLEncoding.DecodeString(authB64)
		if err != nil {
			return nil, fmt.Errorf("decode auth: %w", err)
		}
	}

	// 5. Derive IKM via HKDF
	// key_info = "WebPush: info\0" || ua_public || as_public
	keyInfo := []byte("WebPush: info\x00")
	keyInfo = append(keyInfo, userPubKeyRaw...)
	keyInfo = append(keyInfo, ephemPubUncompressed...)

	// PRK = HKDF-Extract(auth_secret, shared_secret)
	prk := hkdfExtract(authSecret, sharedSecret)
	// IKM = HKDF-Expand(PRK, key_info, 32)
	ikm := hkdfExpand(sha256.New, prk, keyInfo, 32)

	// 6. Generate random salt (16 bytes)
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}

	// 7. Derive CEK and nonce
	// CEK: HKDF-Expand(PRK, "Content-Encoding: aes128gcm\0", 16)
	cekInfo := []byte("Content-Encoding: aes128gcm\x00")
	cek := hkdfExpand(sha256.New, prk, cekInfo, 16)

	// Nonce: PRK_nonce = HKDF-Extract(salt, IKM); nonce = HKDF-Expand(PRK_nonce, "Content-Encoding: nonce\0", 12)
	prkNonce := hkdfExtract(salt, ikm)
	nonceInfo := []byte("Content-Encoding: nonce\x00")
	nonce := hkdfExpand(sha256.New, prkNonce, nonceInfo, 12)

	// 8. Encrypt payload with AES-128-GCM
	// Padding: append 0x02 delimiter (single record, last record)
	paddedPayload := append(payload, 0x02)

	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	ciphertext := aesgcm.Seal(nil, nonce, paddedPayload, nil)

	// 9. Build aes128gcm content encoding record
	// Format: salt(16) || record_size(4) || key_id_len(1) || key_id(varies) || ciphertext
	keyID := ephemPubUncompressed
	recordSize := uint32(4096)

	header := make([]byte, 0, 21+len(keyID)+len(ciphertext))
	header = append(header, salt...)
	header = append(header, byte(recordSize>>24), byte(recordSize>>16), byte(recordSize>>8), byte(recordSize))
	header = append(header, byte(len(keyID)))
	header = append(header, keyID...)
	header = append(header, ciphertext...)

	return header, nil
}

// ─── HKDF helpers (RFC 5869) ─────────────────────────────

// hkdfExtract implements HKDF-Extract: PRK = HMAC-Hash(salt, IKM)
func hkdfExtract(salt, ikm []byte) []byte {
	if len(salt) == 0 {
		salt = make([]byte, 32) // SHA-256 output size
	}
	mac := hmac.New(sha256.New, salt)
	mac.Write(ikm)
	return mac.Sum(nil)
}

// hkdfExpand implements HKDF-Expand: OKM = T(1) || T(2) || ... || T(N) truncated to L
func hkdfExpand(hashFunc func() hash.Hash, prk, info []byte, length int) []byte {
	hashLen := 32 // SHA-256
	n := (length + hashLen - 1) / hashLen
	if n > 255 {
		n = 255
	}

	var t, output []byte
	for i := 1; i <= n; i++ {
		mac := hmac.New(hashFunc, prk)
		mac.Write(t)
		mac.Write(info)
		mac.Write([]byte{byte(i)})
		t = mac.Sum(nil)
		output = append(output, t...)
	}
	if len(output) > length {
		output = output[:length]
	}
	return output
}

// ─── sendWebPush: Full HTTP Send ─────────────────────────

// sendWebPush sends a Web Push notification to a single subscription endpoint.
// Returns true if the subscription should be cleaned up (410 Gone / 404 Not Found).
func sendWebPush(ctx context.Context, endpoint, p256dh, auth, privateKeyB64, publicKeyB64, subject string, payload []byte) bool {
	// 1. Parse endpoint to get origin
	parsedURL, err := url.Parse(endpoint)
	if err != nil {
		slog.Warn("webpush: invalid endpoint", "error", err)
		return true
	}
	origin := parsedURL.Scheme + "://" + parsedURL.Host

	// 2. Convert VAPID private key
	priv, err := vapidPrivateKeyToECDSA(privateKeyB64)
	if err != nil {
		slog.Warn("webpush: invalid VAPID key", "error", err)
		return true
	}

	// 3. Create VAPID JWT
	jwt, pubKeyB64, err := createVAPIDJWT(priv, origin, subject)
	if err != nil {
		slog.Warn("webpush: create JWT", "error", err)
		return true
	}

	// 4. Encrypt payload
	encrypted, err := encryptPayload(payload, p256dh, auth, priv)
	if err != nil {
		slog.Warn("webpush: encrypt", "error", err)
		return true
	}

	// 5. Send HTTP POST to the browser push service endpoint
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(encrypted))
	if err != nil {
		slog.Warn("webpush: create request", "error", err)
		return true
	}

	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Authorization", "vapid t="+jwt+", k="+pubKeyB64)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("TTL", "2419200") // 4 weeks

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Warn("webpush: send", "error", err, "endpoint", truncateEndpoint(endpoint))
		return false // network error, don't cleanup
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	// 410 Gone or 404 Not Found means subscription is no longer valid
	if resp.StatusCode == 410 || resp.StatusCode == 404 {
		slog.Info("webpush: subscription expired, cleaning up", "endpoint", truncateEndpoint(endpoint), "status", resp.StatusCode)
		return true
	}

	if resp.StatusCode >= 400 {
		slog.Warn("webpush: error response", "status", resp.StatusCode, "endpoint", truncateEndpoint(endpoint))
	}

	return false
}

// truncateEndpoint truncates the endpoint URL for logging.
func truncateEndpoint(endpoint string) string {
	if len(endpoint) > 50 {
		return endpoint[:50] + "..."
	}
	return endpoint
}

// Ensure strings is used (for the _ import guard)
var _ = strings.TrimSpace
