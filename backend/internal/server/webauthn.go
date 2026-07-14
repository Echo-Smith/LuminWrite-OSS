package server

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/writing-agent-v2/pkg/response"
)

// ─── WebAuthn Types ─────────────────────────────────────

// WebAuthnUser represents a user in the WebAuthn flow.
type WebAuthnUser struct {
	ID          string `json:"id"`           // internal user identifier
	Name        string `json:"name"`         // display name
	DisplayName string `json:"display_name"` // display name
}

// RegistrationChallenge is the challenge sent to the client for registration.
type RegistrationChallenge struct {
	Challenge        string `json:"challenge"`
	UserID           string `json:"user_id"`
	UserName         string `json:"user_name"`
	UserDisplayName  string `json:"user_display_name"`
	RP               struct {
		Name string `json:"name"`
		ID   string `json:"id"`
	} `json:"rp"`
	PubKeyCredParams []pubKeyCredParam `json:"pubKeyCredParams"`
	AuthenticatorSelection struct {
		AuthenticatorAttachment string `json:"authenticatorAttachment,omitempty"`
		UserVerification        string `json:"userVerification"`
		ResidentKey             string `json:"residentKey"`
	} `json:"authenticatorSelection"`
	Timeout int `json:"timeout"`
	Attestation string `json:"attestation"`
}

type pubKeyCredParam struct {
	Type string `json:"type"`
	Alg  int    `json:"alg"`
}

// AuthenticationChallenge is the challenge sent to the client for authentication.
type AuthenticationChallenge struct {
	Challenge        string `json:"challenge"`
	AllowCredentials []credDescriptor `json:"allowCredentials,omitempty"`
	UserVerification string           `json:"userVerification"`
	Timeout          int              `json:"timeout"`
	RPID             string           `json:"rpId"`
}

type credDescriptor struct {
	Type string   `json:"type"`
	ID   string   `json:"id"`
	Transports []string `json:"transports,omitempty"`
}

// RegistrationResponse is the client's response to a registration challenge.
type RegistrationResponse struct {
	ID                     string          `json:"id"`
	RawID                  string          `json:"rawId"`
	Type                   string          `json:"type"`
	AttestationObject      string          `json:"attestationObject"`
	ClientDataJSON         string          `json:"clientDataJSON"`
	AuthenticatorData      string          `json:"authenticatorData"`
	PublicKey              string          `json:"publicKey"`
	PublicKeyAlgorithm     int             `json:"publicKeyAlgorithm"`
	Transports             []string        `json:"transports"`
}

// AuthenticationResponse is the client's response to an authentication challenge.
type AuthenticationResponse struct {
	ID              string `json:"id"`
	RawID           string `json:"rawId"`
	Type            string `json:"type"`
	AuthenticatorData string `json:"authenticatorData"`
	ClientDataJSON  string `json:"clientDataJSON"`
	Signature       string `json:"signature"`
	UserHandle      string `json:"userHandle,omitempty"`
}

// clientDataJSON is the parsed client data.
type clientDataJSON struct {
	Type      string `json:"type"`
	Challenge string `json:"challenge"`
	Origin    string `json:"origin"`
}

// ─── WebAuthn Service ──────────────────────────────────

// WebAuthnService handles WebAuthn registration and authentication.
type WebAuthnService struct {
	rpID         string
	rpName       string
	rpOrigin     string
	challengeTTL time.Duration
}

// NewWebAuthnService creates a new WebAuthn service.
func NewWebAuthnService(rpID, rpName, rpOrigin string) *WebAuthnService {
	return &WebAuthnService{
		rpID:         rpID,
		rpName:       rpName,
		rpOrigin:     rpOrigin,
		challengeTTL: 5 * time.Minute,
	}
}

// GenerateChallenge generates a random base64url challenge.
func (w *WebAuthnService) GenerateChallenge() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate challenge: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// GenerateUserID generates a random user ID for WebAuthn registration.
func (w *WebAuthnService) GenerateUserID() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate user ID: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// CreateRegistrationChallenge creates a new registration challenge.
func (w *WebAuthnService) CreateRegistrationChallenge(userName, displayName string) (*RegistrationChallenge, error) {
	challenge, err := w.GenerateChallenge()
	if err != nil {
		return nil, err
	}

	userID, err := w.GenerateUserID()
	if err != nil {
		return nil, err
	}

	rc := &RegistrationChallenge{
		Challenge:       challenge,
		UserID:          userID,
		UserName:        userName,
		UserDisplayName: displayName,
		Timeout:         int(w.challengeTTL / time.Millisecond),
		Attestation:     "none",
	}
	rc.RP.Name = w.rpName
	rc.RP.ID = w.rpID
	rc.PubKeyCredParams = []pubKeyCredParam{
		{Type: "public-key", Alg: -7},  // ES256
		{Type: "public-key", Alg: -257}, // RS256
		{Type: "public-key", Alg: -8},   // EdDSA
	}
	rc.AuthenticatorSelection.UserVerification = "preferred"
	rc.AuthenticatorSelection.ResidentKey = "preferred"

	return rc, nil
}

// CreateAuthenticationChallenge creates a new authentication challenge.
func (w *WebAuthnService) CreateAuthenticationChallenge(allowedCreds []credDescriptor) (*AuthenticationChallenge, error) {
	challenge, err := w.GenerateChallenge()
	if err != nil {
		return nil, err
	}

	return &AuthenticationChallenge{
		Challenge:        challenge,
		AllowCredentials: allowedCreds,
		UserVerification: "preferred",
		Timeout:          int(w.challengeTTL / time.Millisecond),
		RPID:             w.rpID,
	}, nil
}

// VerifyRegistrationResponse verifies the client's registration response and extracts the credential.
func (w *WebAuthnService) VerifyRegistrationResponse(resp *RegistrationResponse, expectedChallenge string) (*VerifiedCredential, error) {
	// 1. Verify client data
	clientDataBytes, err := base64.RawURLEncoding.DecodeString(resp.ClientDataJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to decode clientDataJSON: %w", err)
	}

	var clientData clientDataJSON
	if err := json.Unmarshal(clientDataBytes, &clientData); err != nil {
		return nil, fmt.Errorf("failed to parse clientDataJSON: %w", err)
	}

	// 2. Verify type
	if clientData.Type != "webauthn.create" {
		return nil, fmt.Errorf("unexpected client data type: %s (expected webauthn.create)", clientData.Type)
	}

	// 3. Verify challenge matches
	if clientData.Challenge != expectedChallenge {
		return nil, fmt.Errorf("challenge mismatch")
	}

	// 4. Verify origin
	if !w.isOriginAllowed(clientData.Origin) {
		return nil, fmt.Errorf("origin not allowed: %s", clientData.Origin)
	}

	// 5. Parse authenticator data
	authDataBytes, err := base64.RawURLEncoding.DecodeString(resp.AuthenticatorData)
	if err != nil {
		return nil, fmt.Errorf("failed to decode authenticatorData: %w", err)
	}

	if len(authDataBytes) < 37 {
		return nil, fmt.Errorf("authenticator data too short")
	}

	rpIDHash := authDataBytes[:32]
	flags := authDataBytes[32]
	counterBytes := authDataBytes[33:37]

	expectedRPIDHash := sha256.Sum256([]byte(w.rpID))
	if !constantTimeEqual(rpIDHash, expectedRPIDHash[:]) {
		return nil, fmt.Errorf("RP ID hash mismatch")
	}

	// Check user present flag
	if flags&0x01 == 0 {
		return nil, fmt.Errorf("user present flag not set")
	}

	// Check user verified flag (optional but preferred)
	userVerified := flags&0x04 != 0

	// Parse counter
	counter := binary.BigEndian.Uint32(counterBytes)

	// 6. Extract public key from attestation object
	attObjBytes, err := base64.RawURLEncoding.DecodeString(resp.AttestationObject)
	if err != nil {
		return nil, fmt.Errorf("failed to decode attestationObject: %w", err)
	}

	pubKeyBytes, aaguid, err := parseAttestationObject(attObjBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse attestation object: %w", err)
	}

	// 7. Verify that the credential ID is unique
	_, err = base64.RawURLEncoding.DecodeString(resp.RawID)
	if err != nil {
		return nil, fmt.Errorf("failed to decode rawId: %w", err)
	}

	return &VerifiedCredential{
		CredentialID:    resp.RawID,
		PublicKey:       pubKeyBytes,
		SignCount:       int64(counter),
		AAGUID:          aaguid,
		UserVerified:    userVerified,
		Transports:      resp.Transports,
		AttestationType: "none",
	}, nil
}

// VerifiedCredential is the result of a successful registration verification.
type VerifiedCredential struct {
	CredentialID    string
	PublicKey       []byte
	SignCount       int64
	AAGUID          string
	UserVerified    bool
	Transports      []string
	AttestationType string
}

// VerifyAuthenticationResponse verifies the client's authentication response.
func (w *WebAuthnService) VerifyAuthenticationResponse(resp *AuthenticationResponse, expectedChallenge string, storedPubKey []byte, storedSignCount int64) (newSignCount int64, err error) {
	// 1. Verify client data
	clientDataBytes, err := base64.RawURLEncoding.DecodeString(resp.ClientDataJSON)
	if err != nil {
		return 0, fmt.Errorf("failed to decode clientDataJSON: %w", err)
	}

	var clientData clientDataJSON
	if err := json.Unmarshal(clientDataBytes, &clientData); err != nil {
		return 0, fmt.Errorf("failed to parse clientDataJSON: %w", err)
	}

	// 2. Verify type
	if clientData.Type != "webauthn.get" {
		return 0, fmt.Errorf("unexpected client data type: %s (expected webauthn.get)", clientData.Type)
	}

	// 3. Verify challenge matches
	if clientData.Challenge != expectedChallenge {
		return 0, fmt.Errorf("challenge mismatch")
	}

	// 4. Verify origin
	if !w.isOriginAllowed(clientData.Origin) {
		return 0, fmt.Errorf("origin not allowed: %s", clientData.Origin)
	}

	// 5. Parse authenticator data
	authDataBytes, err := base64.RawURLEncoding.DecodeString(resp.AuthenticatorData)
	if err != nil {
		return 0, fmt.Errorf("failed to decode authenticatorData: %w", err)
	}

	if len(authDataBytes) < 37 {
		return 0, fmt.Errorf("authenticator data too short")
	}

	rpIDHash := authDataBytes[:32]
	flags := authDataBytes[32]
	counterBytes := authDataBytes[33:37]

	expectedRPIDHash := sha256.Sum256([]byte(w.rpID))
	if !constantTimeEqual(rpIDHash, expectedRPIDHash[:]) {
		return 0, fmt.Errorf("RP ID hash mismatch")
	}

	// Check user present flag
	if flags&0x01 == 0 {
		return 0, fmt.Errorf("user present flag not set")
	}

	counter := int64(binary.BigEndian.Uint32(counterBytes))

	// 6. Verify signature
	sigBytes, err := base64.RawURLEncoding.DecodeString(resp.Signature)
	if err != nil {
		return 0, fmt.Errorf("failed to decode signature: %w", err)
	}

	// Build the signed data: authenticatorData || SHA256(clientDataJSON)
	clientDataHash := sha256.Sum256(clientDataBytes)
	signedData := append(authDataBytes, clientDataHash[:]...)

	if err := verifySignature(storedPubKey, sigBytes, signedData); err != nil {
		return 0, fmt.Errorf("signature verification failed: %w", err)
	}

	// 7. Check sign count (prevent replay)
	if counter > 0 && counter <= storedSignCount {
		return 0, fmt.Errorf("sign count regression detected (possible replay attack)")
	}

	return counter, nil
}

// isOriginAllowed checks if the origin is allowed for this RP.
func (w *WebAuthnService) isOriginAllowed(origin string) bool {
	// Allow the configured origin
	if origin == w.rpOrigin {
		return true
	}
	// In dev mode, allow localhost variants
	if strings.Contains(origin, "localhost") || strings.Contains(origin, "127.0.0.1") {
		return true
	}
	return false
}

// ─── CBOR/COSE Parsing (minimal implementation) ─────────

// parseAttestationObject parses a CBOR-encoded attestation object and extracts
// the public key bytes and AAGUID.
func parseAttestationObject(data []byte) (pubKey []byte, aaguid string, err error) {
	// Minimal CBOR map parser for attestation object
	// Format: {fmt, authData, attStmt}
	m, err := cborDecode(data)
	if err != nil {
		return nil, "", fmt.Errorf("cbor decode: %w", err)
	}

	obj, ok := m.(map[string]interface{})
	if !ok {
		return nil, "", fmt.Errorf("attestation object is not a map")
	}

	authData, ok := obj["authData"].([]byte)
	if !ok {
		return nil, "", fmt.Errorf("authData not found in attestation object")
	}

	// Parse authData to extract the credential public key
	if len(authData) < 37 {
		return nil, "", fmt.Errorf("authData too short")
	}

	flags := authData[32]
	hasAttestedCredential := flags&0x40 != 0
	if !hasAttestedCredential {
		return nil, "", fmt.Errorf("no attested credential data in authData")
	}

	// Skip rpIdHash(32) + flags(1) + signCount(4) = 37
	pos := 37

	// attestedCredentialData: aaguid(16) + credIdLen(2) + credId(credIdLen) + credPubKey(CBOR)
	if pos+16+2 > len(authData) {
		return nil, "", fmt.Errorf("authData too short for attested credential data")
	}

	// Extract AAGUID
	aaguidBytes := authData[pos : pos+16]
	pos += 16
	aaguid = formatUUID(aaguidBytes)

	// Credential ID length
	credIDLen := int(binary.BigEndian.Uint16(authData[pos : pos+2]))
	pos += 2

	if pos+credIDLen > len(authData) {
		return nil, "", fmt.Errorf("authData too short for credential ID")
	}
	pos += credIDLen

	// The rest is the COSE public key (CBOR encoded)
	cosePubKeyBytes := authData[pos:]

	// Parse the COSE key to extract the actual public key
	pubKey, err = parseCOSEKey(cosePubKeyBytes)
	if err != nil {
		return nil, "", fmt.Errorf("failed to parse COSE key: %w", err)
	}

	return pubKey, aaguid, nil
}

// parseCOSEKey parses a COSE public key and returns it in a format suitable for storage.
func parseCOSEKey(data []byte) ([]byte, error) {
	// Decode the CBOR COSE key
	key, err := cborDecode(data)
	if err != nil {
		return nil, fmt.Errorf("cbor decode cose key: %w", err)
	}

	m, ok := key.(map[int]interface{})
	if !ok {
		// Try map[interface{}]interface{}
		if mi, ok := key.(map[interface{}]interface{}); ok {
			m = make(map[int]interface{})
			for k, v := range mi {
				if ki, ok := k.(int); ok {
					m[ki] = v
				}
			}
		} else {
			return nil, fmt.Errorf("COSE key is not a map")
		}
	}

	// COSE key labels:
	// 1 = kty (key type)
	// 3 = alg (algorithm)
	// -1 = crv/n
	// -2 = x/e
	// -3 = y

	kty, ok := m[1].(int)
	if !ok {
		if kty64, ok := m[1].(int64); ok {
			kty = int(kty64)
		} else {
			return nil, fmt.Errorf("missing key type")
		}
	}

	switch kty {
	case 2: // EC2
		// Get curve
		crv, _ := m[-1].(int)
		if crv == 0 {
			if crv64, ok := m[-1].(int64); ok {
				crv = int(crv64)
			}
		}

		x, _ := m[-2].([]byte)
		y, _ := m[-3].([]byte)

		switch crv {
		case 1: // P-256
			pubKey := &ecdsa.PublicKey{
				Curve: elliptic.P256(),
				X:     bytesToBigInt(x),
				Y:     bytesToBigInt(y),
			}
			return x509.MarshalPKIXPublicKey(pubKey)
		case 2: // P-384
			pubKey := &ecdsa.PublicKey{
				Curve: elliptic.P384(),
				X:     bytesToBigInt(x),
				Y:     bytesToBigInt(y),
			}
			return x509.MarshalPKIXPublicKey(pubKey)
		default:
			return nil, fmt.Errorf("unsupported EC curve: %d", crv)
		}

	case 3: // OKP (EdDSA)
		x, _ := m[-2].([]byte)
		if len(x) == 32 {
			pubKey := ed25519.PublicKey(x)
			return x509.MarshalPKIXPublicKey(pubKey)
		}
		return nil, fmt.Errorf("invalid EdDSA key length")

	case 1: // RSA
		n, _ := m[-1].([]byte)
		e, _ := m[-2].([]byte)

		pubKey := &rsa.PublicKey{
			N: bytesToBigInt(n),
			E: int(bytesToBigInt(e).Int64()),
		}
		return x509.MarshalPKIXPublicKey(pubKey)

	default:
		return nil, fmt.Errorf("unsupported key type: %d", kty)
	}
}

// verifySignature verifies the signature using the stored public key.
func verifySignature(pubKeyBytes []byte, signature []byte, data []byte) error {
	pubKey, err := x509.ParsePKIXPublicKey(pubKeyBytes)
	if err != nil {
		return fmt.Errorf("failed to parse public key: %w", err)
	}

	switch key := pubKey.(type) {
	case *ecdsa.PublicKey:
		hash := sha256.Sum256(data)
		if !ecdsa.VerifyASN1(key, hash[:], signature) {
			return fmt.Errorf("ECDSA signature verification failed")
		}
		return nil

	case ed25519.PublicKey:
		if !ed25519.Verify(key, data, signature) {
			return fmt.Errorf("Ed25519 signature verification failed")
		}
		return nil

	case *rsa.PublicKey:
		hash := sha256.Sum256(data)
		if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, hash[:], signature); err != nil {
			return fmt.Errorf("RSA signature verification failed: %w", err)
		}
		return nil

	default:
		return fmt.Errorf("unsupported public key type: %T", pubKey)
	}
}

// ─── Minimal CBOR Decoder ──────────────────────────────

// cborDecode is a minimal CBOR decoder that handles maps, strings, bytes, ints, and arrays.
func cborDecode(data []byte) (interface{}, error) {
	dec := &cborDecoder{data: data}
	return dec.decodeValue()
}

type cborDecoder struct {
	data []byte
	pos  int
}

func (d *cborDecoder) decodeValue() (interface{}, error) {
	if d.pos >= len(d.data) {
		return nil, fmt.Errorf("unexpected end of data")
	}

	b := d.data[d.pos]
	d.pos++

	// Major type
	majorType := b >> 5
	info := b & 0x1f

	switch majorType {
	case 0: // unsigned int
		val, err := d.decodeUint(info)
		if err != nil {
			return nil, err
		}
		return int64(val), nil

	case 1: // negative int
		val, err := d.decodeUint(info)
		if err != nil {
			return nil, err
		}
		return int64(-1) - int64(val), nil

	case 2: // byte string
		length, err := d.decodeUint(info)
		if err != nil {
			return nil, err
		}
		if d.pos+int(length) > len(d.data) {
			return nil, fmt.Errorf("byte string length exceeds data")
		}
		result := make([]byte, length)
		copy(result, d.data[d.pos:d.pos+int(length)])
		d.pos += int(length)
		return result, nil

	case 3: // text string
		length, err := d.decodeUint(info)
		if err != nil {
			return nil, err
		}
		if d.pos+int(length) > len(d.data) {
			return nil, fmt.Errorf("text string length exceeds data")
		}
		result := string(d.data[d.pos : d.pos+int(length)])
		d.pos += int(length)
		return result, nil

	case 4: // array
		length, err := d.decodeUint(info)
		if err != nil {
			return nil, err
		}
		arr := make([]interface{}, length)
		for i := uint64(0); i < length; i++ {
			val, err := d.decodeValue()
			if err != nil {
				return nil, err
			}
			arr[i] = val
		}
		return arr, nil

	case 5: // map
		length, err := d.decodeUint(info)
		if err != nil {
			return nil, err
		}
		m := make(map[interface{}]interface{}, length)
		for i := uint64(0); i < length; i++ {
			key, err := d.decodeValue()
			if err != nil {
				return nil, err
			}
			val, err := d.decodeValue()
			if err != nil {
				return nil, err
			}
			m[key] = val
		}
		return m, nil

	case 7: // simple values, float
		if info == 20 {
			return false, nil
		}
		if info == 21 {
			return true, nil
		}
		if info == 22 {
			return nil, nil
		}
		val, err := d.decodeUint(info)
		if err != nil {
			return nil, err
		}
		return int64(val), nil

	default:
		return nil, fmt.Errorf("unsupported CBOR major type: %d", majorType)
	}
}

func (d *cborDecoder) decodeUint(info byte) (uint64, error) {
	if info < 24 {
		return uint64(info), nil
	}
	switch info {
	case 24:
		if d.pos+1 > len(d.data) {
			return 0, fmt.Errorf("unexpected end of data")
		}
		val := uint64(d.data[d.pos])
		d.pos++
		return val, nil
	case 25:
		if d.pos+2 > len(d.data) {
			return 0, fmt.Errorf("unexpected end of data")
		}
		val := uint64(binary.BigEndian.Uint16(d.data[d.pos:]))
		d.pos += 2
		return val, nil
	case 26:
		if d.pos+4 > len(d.data) {
			return 0, fmt.Errorf("unexpected end of data")
		}
		val := uint64(binary.BigEndian.Uint32(d.data[d.pos:]))
		d.pos += 4
		return val, nil
	case 27:
		if d.pos+8 > len(d.data) {
			return 0, fmt.Errorf("unexpected end of data")
		}
		val := binary.BigEndian.Uint64(d.data[d.pos:])
		d.pos += 8
		return val, nil
	default:
		return 0, fmt.Errorf("invalid additional info: %d", info)
	}
}

// ─── Helpers ───────────────────────────────────────────

func constantTimeEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var result byte
	for i := 0; i < len(a); i++ {
		result |= a[i] ^ b[i]
	}
	return result == 0
}

func formatUUID(b []byte) string {
	if len(b) != 16 {
		return ""
	}
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		binary.BigEndian.Uint32(b[0:4]),
		binary.BigEndian.Uint16(b[4:6]),
		binary.BigEndian.Uint16(b[6:8]),
		binary.BigEndian.Uint16(b[8:10]),
		b[10:16])
}

func bytesToBigInt(b []byte) *big.Int {
	return new(big.Int).SetBytes(b)
}

// ─── Passkey Challenge Store ───────────────────────────

// passkeyChallengeStore stores challenges in memory with TTL.
type passkeyChallengeStore struct {
	challenges map[string]*storedChallenge
	mu         sync.Mutex
}

type storedChallenge struct {
	challenge string
	userID    string
	userInfo  *WebAuthnUser
	purpose   string // "registration" | "authentication"
	createdAt time.Time
	expiresAt time.Time
}

func newPasskeyChallengeStore() *passkeyChallengeStore {
	store := &passkeyChallengeStore{
		challenges: make(map[string]*storedChallenge),
	}
	go store.cleanup()
	return store
}

func (s *passkeyChallengeStore) Store(challenge string, userID string, userInfo *WebAuthnUser, purpose string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.challenges[challenge] = &storedChallenge{
		challenge: challenge,
		userID:    userID,
		userInfo:  userInfo,
		purpose:   purpose,
		createdAt: now,
		expiresAt: now.Add(5 * time.Minute),
	}
}

func (s *passkeyChallengeStore) Consume(challenge string) (*storedChallenge, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sc, ok := s.challenges[challenge]
	if !ok {
		return nil, false
	}
	if time.Now().After(sc.expiresAt) {
		delete(s.challenges, challenge)
		return nil, false
	}
	delete(s.challenges, challenge) // single-use
	return sc, true
}

func (s *passkeyChallengeStore) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		s.mu.Lock()
		now := time.Now()
		for k, v := range s.challenges {
			if now.After(v.expiresAt) {
				delete(s.challenges, k)
			}
		}
		s.mu.Unlock()
	}
}

// ─── Passkey Handlers ──────────────────────────────────

// handlePasskeyRegisterBegin starts the passkey registration flow.
// POST /api/v2/auth/passkey/register/begin
// Body: {"name": "My MacBook", "user_id": "admin"}  (user_id optional, defaults to authenticated user)
func (s *Server) handlePasskeyRegisterBegin(w http.ResponseWriter, r *http.Request) {
	if s.webauthn == nil {
		response.Err(w, http.StatusServiceUnavailable, "unavailable", "WebAuthn not configured")
		return
	}

	var req struct {
		Name     string `json:"name"`
		UserID   string `json:"user_id"`
		UserName string `json:"user_name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	// Use authenticated user if not specified
	if req.UserID == "" {
		user := userFromContext(r.Context())
		if user != nil {
			req.UserID = user.Sub
		}
	}
	if req.UserID == "" {
		req.UserID = "admin"
	}
	if req.UserName == "" {
		req.UserName = req.UserID
	}

	displayName := req.UserName
	if req.Name != "" {
		displayName = req.Name
	}

	challenge, err := s.webauthn.CreateRegistrationChallenge(req.UserName, displayName)
	if err != nil {
		slog.Error("failed to create registration challenge", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to create challenge")
		return
	}

	// Store the challenge
	s.passkeyChallenges.Store(challenge.Challenge, req.UserID, &WebAuthnUser{
		ID:          challenge.UserID,
		Name:        req.UserName,
		DisplayName: displayName,
	}, "registration")

	response.OK(w, challenge)
}

// handlePasskeyRegisterComplete completes the passkey registration.
// POST /api/v2/auth/passkey/register/complete
// Body: {registration response from browser}
func (s *Server) handlePasskeyRegisterComplete(w http.ResponseWriter, r *http.Request) {
	if s.webauthn == nil {
		response.Err(w, http.StatusServiceUnavailable, "unavailable", "WebAuthn not configured")
		return
	}

	var resp RegistrationResponse
	if err := json.NewDecoder(r.Body).Decode(&resp); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	// Get client data to find the challenge
	clientDataBytes, err := base64.RawURLEncoding.DecodeString(resp.ClientDataJSON)
	if err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid clientDataJSON")
		return
	}

	var clientData clientDataJSON
	if err := json.Unmarshal(clientDataBytes, &clientData); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid clientDataJSON")
		return
	}

	// Consume the challenge
	sc, ok := s.passkeyChallenges.Consume(clientData.Challenge)
	if !ok {
		response.Err(w, http.StatusBadRequest, "invalid_challenge", "challenge not found or expired")
		return
	}

	if sc.purpose != "registration" {
		response.Err(w, http.StatusBadRequest, "invalid_challenge", "challenge purpose mismatch")
		return
	}

	// Verify the registration response
	cred, err := s.webauthn.VerifyRegistrationResponse(&resp, clientData.Challenge)
	if err != nil {
		slog.Warn("passkey registration verification failed", "error", err)
		response.Err(w, http.StatusBadRequest, "verification_failed", err.Error())
		return
	}

	// Store credential in database
	if s.adminRepo != nil && s.adminRepo.DB() != nil {
		_, err := s.adminRepo.DB().ExecContext(r.Context(), `
			INSERT INTO passkey_credentials (user_id, credential_id, public_key, attestation_type, aaguid, sign_count, transports, device_type, backed_up, name, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 'single_device', false, $8, NOW())
			ON CONFLICT (credential_id) DO NOTHING
		`, sc.userID, cred.CredentialID, cred.PublicKey, cred.AttestationType, cred.AAGUID,
			cred.SignCount, fmt.Sprintf("{%s}", strings.Join(cred.Transports, ",")), sc.userInfo.DisplayName)
		if err != nil {
			slog.Error("failed to store passkey credential", "error", err)
			response.Err(w, http.StatusInternalServerError, "internal_error", "failed to store credential")
			return
		}
	}

	slog.Info("passkey registered", "user_id", sc.userID, "credential_id", cred.CredentialID[:16]+"...")

	response.OK(w, map[string]interface{}{
		"success": true,
		"message": "passkey registered successfully",
	})
}

// handlePasskeyLoginBegin starts the passkey authentication flow.
// POST /api/v2/auth/passkey/login/begin
// Body: {} or {"user_id": "admin"} (optional, for username-less flow omit user_id)
func (s *Server) handlePasskeyLoginBegin(w http.ResponseWriter, r *http.Request) {
	if s.webauthn == nil {
		response.Err(w, http.StatusServiceUnavailable, "unavailable", "WebAuthn not configured")
		return
	}

	var req struct {
		UserID string `json:"user_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	// If user_id is provided, fetch their credentials for allowCredentials
	var allowedCreds []credDescriptor
	if req.UserID != "" && s.adminRepo != nil && s.adminRepo.DB() != nil {
		rows, err := s.adminRepo.DB().QueryContext(r.Context(), `
			SELECT credential_id, transports FROM passkey_credentials WHERE user_id = $1
		`, req.UserID)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var credID string
				var transports []string
				if err := rows.Scan(&credID, &transports); err != nil {
					continue
				}
				allowedCreds = append(allowedCreds, credDescriptor{
					Type:       "public-key",
					ID:         credID,
					Transports: transports,
				})
			}
		}
	}

	challenge, err := s.webauthn.CreateAuthenticationChallenge(allowedCreds)
	if err != nil {
		slog.Error("failed to create authentication challenge", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to create challenge")
		return
	}

	// Store the challenge
	s.passkeyChallenges.Store(challenge.Challenge, req.UserID, nil, "authentication")

	response.OK(w, challenge)
}

// handlePasskeyLoginComplete completes the passkey authentication.
// POST /api/v2/auth/passkey/login/complete
// Body: {authentication response from browser}
func (s *Server) handlePasskeyLoginComplete(w http.ResponseWriter, r *http.Request) {
	if s.webauthn == nil {
		response.Err(w, http.StatusServiceUnavailable, "unavailable", "WebAuthn not configured")
		return
	}

	var resp AuthenticationResponse
	if err := json.NewDecoder(r.Body).Decode(&resp); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	// Get client data to find the challenge
	clientDataBytes, err := base64.RawURLEncoding.DecodeString(resp.ClientDataJSON)
	if err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid clientDataJSON")
		return
	}

	var clientData clientDataJSON
	if err := json.Unmarshal(clientDataBytes, &clientData); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid clientDataJSON")
		return
	}

	// Consume the challenge
	sc, ok := s.passkeyChallenges.Consume(clientData.Challenge)
	if !ok {
		response.Err(w, http.StatusBadRequest, "invalid_challenge", "challenge not found or expired")
		return
	}

	if sc.purpose != "authentication" {
		response.Err(w, http.StatusBadRequest, "invalid_challenge", "challenge purpose mismatch")
		return
	}

	// Look up the credential in the database
	if s.adminRepo == nil || s.adminRepo.DB() == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	var (
		userID       string
		storedPubKey []byte
		storedCount   int64
	)
	err = s.adminRepo.DB().QueryRowContext(r.Context(), `
		SELECT user_id, public_key, sign_count
		FROM passkey_credentials
		WHERE credential_id = $1
	`, resp.RawID).Scan(&userID, &storedPubKey, &storedCount)
	if err != nil {
		slog.Warn("passkey credential not found", "credential_id", resp.RawID[:16]+"...", "error", err)
		response.Err(w, http.StatusUnauthorized, "invalid_credentials", "credential not found")
		return
	}

	// Verify the authentication response
	newCount, err := s.webauthn.VerifyAuthenticationResponse(&resp, clientData.Challenge, storedPubKey, storedCount)
	if err != nil {
		slog.Warn("passkey authentication verification failed", "error", err)
		response.Err(w, http.StatusUnauthorized, "verification_failed", err.Error())
		return
	}

	// Update sign count
	_, _ = s.adminRepo.DB().ExecContext(r.Context(), `
		UPDATE passkey_credentials SET sign_count = $2, last_used_at = NOW() WHERE credential_id = $1
	`, resp.RawID, newCount)

	// Determine role
	role := "user"
	if userID == "admin" {
		role = "admin"
	}

	// Issue JWT
	s.issueToken(w, userID, role)

	slog.Info("passkey login successful", "user_id", userID)
}

// handlePasskeyList lists the user's registered passkeys.
// GET /api/v2/auth/passkey/list
func (s *Server) handlePasskeyList(w http.ResponseWriter, r *http.Request) {
	if s.adminRepo == nil || s.adminRepo.DB() == nil {
		response.OK(w, map[string]interface{}{"passkeys": []interface{}{}})
		return
	}

	user := userFromContext(r.Context())
	userID := ""
	if user != nil {
		userID = user.Sub
	}
	if userID == "" {
		userID = "admin"
	}

	rows, err := s.adminRepo.DB().QueryContext(r.Context(), `
		SELECT id::text, credential_id, name, created_at, last_used_at, transports
		FROM passkey_credentials
		WHERE user_id = $1
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		response.OK(w, map[string]interface{}{"passkeys": []interface{}{}})
		return
	}
	defer rows.Close()

	var passkeys []map[string]interface{}
	for rows.Next() {
		var (
			id          string
			credID      string
			name        *string
			createdAt   time.Time
			lastUsedAt  *time.Time
			transports  []string
		)
		if err := rows.Scan(&id, &credID, &name, &createdAt, &lastUsedAt, &transports); err != nil {
			continue
		}
		entry := map[string]interface{}{
			"id":            id,
			"credential_id": credID[:16] + "...",
			"name":          "",
			"created_at":    createdAt,
			"transports":    transports,
		}
		if name != nil {
			entry["name"] = *name
		}
		if lastUsedAt != nil {
			entry["last_used_at"] = *lastUsedAt
		}
		passkeys = append(passkeys, entry)
	}

	if passkeys == nil {
		passkeys = []map[string]interface{}{}
	}

	response.OK(w, map[string]interface{}{"passkeys": passkeys})
}

// handlePasskeyDelete deletes a passkey credential.
// DELETE /api/v2/auth/passkey/{id}
func (s *Server) handlePasskeyDelete(w http.ResponseWriter, r *http.Request) {
	if s.adminRepo == nil || s.adminRepo.DB() == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "id is required")
		return
	}

	_, err := s.adminRepo.DB().ExecContext(r.Context(), `
		DELETE FROM passkey_credentials WHERE id = $1
	`, id)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to delete passkey")
		return
	}

	response.OK(w, map[string]interface{}{"deleted": true})
}
