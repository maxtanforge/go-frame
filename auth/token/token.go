// Package token issues and verifies one deliberately narrow encrypted access-token
// profile: ES256 signed JWT inside a dir/A256GCM compact JWE. It is not a general
// JWT parser and is not compatible with arbitrary third-party JWT profiles.
package token

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/maxtanforge/go-frame/auth/principal"
)

// Default TTL, clock skew and payload bounds used when an option leaves them zero.
const (
	DefaultTTL             = 15 * time.Minute
	DefaultClockSkew       = 30 * time.Second
	MaxTokenBytes          = 16 * 1024
	MaxPayloadBytes        = 4 * 1024
	maxDate          int64 = 253402300799 // final whole second of year 9999
)

// Credential errors intentionally contain no token, key, claim, or JOSE detail.
var (
	ErrInvalid     = fmt.Errorf("token: invalid: %w", principal.ErrUnauthenticated)
	ErrExpired     = fmt.Errorf("token: expired: %w", principal.ErrUnauthenticated)
	ErrNotYetValid = fmt.Errorf("token: not yet valid: %w", principal.ErrUnauthenticated)
)

// IssuerOptions are constructor inputs, not a serializable key configuration.
// A zero TTL or MaxTTL means DefaultTTL; zero ClockSkew means DefaultClockSkew.
// Clock must be safe for concurrent use. Keys are validated and copied.
type IssuerOptions struct {
	Issuer, Audience, SigningKeyID, EncryptionKeyID string
	SigningKey                                      *ecdsa.PrivateKey
	EncryptionKey                                   []byte
	TTL, MaxTTL, ClockSkew                          time.Duration
	Clock                                           func() time.Time
}

// VerifierOptions accepts at most 16 keys per independent kid namespace.
// Retain old keys during rotation until their last token's lifetime plus skew.
type VerifierOptions struct {
	Issuer, Audience  string
	SigningKeys       map[string]*ecdsa.PublicKey
	EncryptionKeys    map[string][]byte
	MaxTTL, ClockSkew time.Duration
	Clock             func() time.Time
}

// Result contains a credential. Do not log or expose it in audit records.
type Result struct {
	AccessToken string    `json:"access_token"`
	TokenType   string    `json:"token_type"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type policy struct {
	issuer, audience string
	maxTTL, skew     time.Duration
	clock            func() time.Time
}

// Issuer is immutable after construction and safe for concurrent use.
type Issuer struct {
	policy
	ttl       time.Duration
	signer    jose.Signer
	encrypter jose.Encrypter
}

// Verifier has no private signing key or issuance capability.
type Verifier struct {
	policy
	signing    map[string]*ecdsa.PublicKey
	encryption map[string][]byte
}

func newPolicy(issuer, audience string, maxTTL, skew time.Duration, clock func() time.Time) (policy, error) {
	if !validText(issuer, 256) || !validText(audience, 256) {
		return policy{}, errors.New("token: issuer and audience must be nonempty bounded text")
	}
	if maxTTL == 0 {
		maxTTL = DefaultTTL
	}
	if skew == 0 {
		skew = DefaultClockSkew
	}
	if clock == nil {
		clock = time.Now
	}
	if maxTTL < time.Second || maxTTL%time.Second != 0 || skew < 0 || skew > 5*time.Minute || skew%time.Second != 0 {
		return policy{}, errors.New("token: invalid lifetime or clock skew")
	}
	return policy{issuer, audience, maxTTL, skew, clock}, nil
}

// NewIssuer validates keys and options, then returns a stateless issuer.
func NewIssuer(o IssuerOptions) (*Issuer, error) {
	p, err := newPolicy(o.Issuer, o.Audience, o.MaxTTL, o.ClockSkew, o.Clock)
	if err != nil {
		return nil, err
	}
	if o.TTL == 0 {
		o.TTL = DefaultTTL
	}
	if o.TTL < time.Second || o.TTL > p.maxTTL || o.TTL%time.Second != 0 {
		return nil, errors.New("token: invalid TTL")
	}
	if !validText(o.SigningKeyID, 128) || !validText(o.EncryptionKeyID, 128) || len(o.EncryptionKey) != 32 || allZero(o.EncryptionKey) {
		return nil, errors.New("token: invalid key ID or encryption key")
	}
	key, err := copyPrivate(o.SigningKey)
	if err != nil {
		return nil, err
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: key}, (&jose.SignerOptions{}).WithType("frame-access+jwt").WithHeader("kid", o.SigningKeyID))
	if err != nil {
		return nil, fmt.Errorf("token: create signer: %w", err)
	}
	enc, err := jose.NewEncrypter(jose.A256GCM, jose.Recipient{Algorithm: jose.DIRECT, Key: bytes.Clone(o.EncryptionKey), KeyID: o.EncryptionKeyID}, (&jose.EncrypterOptions{}).WithContentType("JWT"))
	if err != nil {
		return nil, fmt.Errorf("token: create encrypter: %w", err)
	}
	return &Issuer{policy: p, ttl: o.TTL, signer: signer, encrypter: enc}, nil
}

// NewVerifier validates keys and options, then returns a stateless verifier.
func NewVerifier(o VerifierOptions) (*Verifier, error) {
	p, err := newPolicy(o.Issuer, o.Audience, o.MaxTTL, o.ClockSkew, o.Clock)
	if err != nil {
		return nil, err
	}
	if len(o.SigningKeys) < 1 || len(o.SigningKeys) > 16 || len(o.EncryptionKeys) < 1 || len(o.EncryptionKeys) > 16 {
		return nil, errors.New("token: require 1 to 16 keys per key set")
	}
	v := &Verifier{policy: p, signing: make(map[string]*ecdsa.PublicKey), encryption: make(map[string][]byte)}
	for id, k := range o.SigningKeys {
		if !validText(id, 128) {
			return nil, errors.New("token: invalid verification key")
		}
		key, err := copyPublic(k)
		if err != nil {
			return nil, err
		}
		v.signing[id] = key
	}
	for id, k := range o.EncryptionKeys {
		if !validText(id, 128) || len(k) != 32 || allZero(k) {
			return nil, errors.New("token: invalid decryption key")
		}
		v.encryption[id] = bytes.Clone(k)
	}
	return v, nil
}

func copyPrivate(k *ecdsa.PrivateKey) (key *ecdsa.PrivateKey, err error) {
	invalid := errors.New("token: invalid P-256 signing key")
	if k == nil || k.Curve != elliptic.P256() {
		return nil, invalid
	}
	// Standard encoders validate values but can panic on nil legacy fields in
	// malformed caller keys. Keep recovery confined to this key conversion.
	defer func() {
		if recover() != nil {
			key, err = nil, invalid
		}
	}()
	raw, err := k.Bytes()
	if err != nil {
		return nil, invalid
	}
	key, err = ecdsa.ParseRawPrivateKey(elliptic.P256(), raw)
	if err != nil {
		return nil, invalid
	}
	// Bytes validates the scalar and point independently; parsing derives the
	// point from the scalar, allowing us to reject inconsistent pairs as well.
	if !key.PublicKey.Equal(&k.PublicKey) {
		return nil, errors.New("token: inconsistent signing key")
	}
	return key, nil
}

// allZero rejects the accidental make([]byte, 32) key: a real 256-bit key has
// negligible probability of being all zero, and issuing under one encrypts
// tokens under a guessable key.
func allZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}
func copyPublic(k *ecdsa.PublicKey) (key *ecdsa.PublicKey, err error) {
	invalid := errors.New("token: invalid verification key")
	if k == nil || k.Curve != elliptic.P256() {
		return nil, invalid
	}
	// As with private keys, nil legacy coordinates can panic in Bytes.
	defer func() {
		if recover() != nil {
			key, err = nil, invalid
		}
	}()
	raw, err := k.Bytes()
	if err != nil {
		return nil, invalid
	}
	key, err = ecdsa.ParseUncompressedPublicKey(elliptic.P256(), raw)
	if err != nil {
		return nil, invalid
	}
	return key, nil
}
func validText(s string, maxLen int) bool {
	return len(s) <= maxLen && (principal.Principal{ID: "x", Issuer: s}).Validate() == nil
}

type claims struct {
	Issuer    string `json:"iss"`
	Audience  string `json:"aud"`
	Subject   string `json:"sub"`
	Name      string `json:"name,omitempty"`
	ID        string `json:"jti"`
	Expires   *int64 `json:"exp"`
	Issued    *int64 `json:"iat"`
	NotBefore *int64 `json:"nbf"`
}

// Issue takes only ID and Name from the input. The issuer owns Issuer, TokenID,
// all times, and the audience; input provenance fields cannot override them.
func (i *Issuer) Issue(ctx context.Context, p principal.Principal) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if i == nil {
		return Result{}, errors.New("token: nil issuer")
	}
	p.Issuer = i.issuer
	p.TokenID = ""
	if p.Validate() != nil {
		return Result{}, ErrInvalid
	}
	now := i.clock().Unix()
	exp := now + int64(i.ttl/time.Second)
	if now < 0 || now > maxDate || exp > maxDate {
		return Result{}, errors.New("token: clock outside supported range")
	}
	nonce := make([]byte, 24)
	if _, err := rand.Read(nonce); err != nil {
		return Result{}, fmt.Errorf("token: generate ID: %w", err)
	}
	c := claims{Issuer: i.issuer, Audience: i.audience, Subject: p.ID, Name: p.Name, ID: base64.RawURLEncoding.EncodeToString(nonce), Expires: &exp, Issued: &now, NotBefore: &now}
	payload, err := json.Marshal(c)
	if err != nil {
		return Result{}, err
	}
	if len(payload) > MaxPayloadBytes {
		return Result{}, ErrInvalid
	}
	signed, err := i.signer.Sign(payload)
	if err != nil {
		return Result{}, fmt.Errorf("token: sign: %w", err)
	}
	compact, err := signed.CompactSerialize()
	if err != nil {
		return Result{}, err
	}
	encrypted, err := i.encrypter.Encrypt([]byte(compact))
	if err != nil {
		return Result{}, fmt.Errorf("token: encrypt: %w", err)
	}
	raw, err := encrypted.CompactSerialize()
	if err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	return Result{AccessToken: raw, TokenType: "Bearer", ExpiresAt: time.Unix(exp, 0).UTC()}, nil
}

// Verify authenticates both layers before decoding claims. All credential
// failures wrap principal.ErrUnauthenticated; context errors remain context errors.
func (v *Verifier) Verify(ctx context.Context, raw string) (principal.Principal, error) {
	fail := func(err error) (principal.Principal, error) { return principal.Principal{}, err }
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if v == nil {
		return fail(errors.New("token: nil verifier"))
	}
	if len(raw) > MaxTokenBytes {
		return fail(ErrInvalid)
	}
	outer := strings.Split(raw, ".")
	if len(outer) != 5 || outer[1] != "" {
		return fail(ErrInvalid)
	}
	if !canonicalSegments(outer, 1) {
		return fail(ErrInvalid)
	}
	kid, err := header(outer[0], map[string]string{"alg": "dir", "enc": "A256GCM", "cty": "JWT"})
	if err != nil {
		return fail(ErrInvalid)
	}
	key, ok := v.encryption[kid]
	if !ok {
		return fail(ErrInvalid)
	}
	encrypted, err := jose.ParseEncryptedCompact(raw, []jose.KeyAlgorithm{jose.DIRECT}, []jose.ContentEncryption{jose.A256GCM})
	if err != nil {
		return fail(ErrInvalid)
	}
	nested, err := encrypted.Decrypt(key)
	if err != nil || len(nested) > MaxTokenBytes {
		return fail(ErrInvalid)
	}
	inner := strings.Split(string(nested), ".")
	if len(inner) != 3 || base64.RawURLEncoding.DecodedLen(len(inner[1])) > MaxPayloadBytes {
		return fail(ErrInvalid)
	}
	if !canonicalSegments(inner, -1) {
		return fail(ErrInvalid)
	}
	kid, err = header(inner[0], map[string]string{"alg": "ES256", "typ": "frame-access+jwt"})
	if err != nil {
		return fail(ErrInvalid)
	}
	public, ok := v.signing[kid]
	if !ok {
		return fail(ErrInvalid)
	}
	signed, err := jose.ParseSignedCompact(string(nested), []jose.SignatureAlgorithm{jose.ES256})
	if err != nil {
		return fail(ErrInvalid)
	}
	payload, err := signed.Verify(public)
	if err != nil || len(payload) > MaxPayloadBytes {
		return fail(ErrInvalid)
	}
	fields, err := object(payload)
	if err != nil {
		return fail(ErrInvalid)
	}
	for field := range fields {
		switch field {
		case "iss", "aud", "sub", "name", "jti", "exp", "iat", "nbf":
		default:
			return fail(ErrInvalid)
		}
	}
	var c claims
	if err = json.Unmarshal(payload, &c); err != nil {
		return fail(ErrInvalid)
	}
	if c.Issuer != v.issuer || c.Audience != v.audience || c.ID == "" || c.Expires == nil || c.Issued == nil || c.NotBefore == nil {
		return fail(ErrInvalid)
	}
	p := principal.Principal{ID: c.Subject, Name: c.Name, Issuer: c.Issuer, TokenID: c.ID}
	if p.Validate() != nil {
		return fail(ErrInvalid)
	}
	exp, iat, nbf := *c.Expires, *c.Issued, *c.NotBefore
	if iat < 0 || iat > maxDate || nbf < iat || nbf >= exp || exp > maxDate || exp <= iat || exp-iat > int64(v.maxTTL/time.Second) {
		return fail(ErrInvalid)
	}
	now := v.clock()
	if now.Unix() < 0 || now.Unix() > maxDate {
		return fail(errors.New("token: clock outside supported range"))
	}
	if !now.Before(time.Unix(exp, 0).Add(v.skew)) {
		return fail(ErrExpired)
	}
	if now.Add(v.skew).Before(time.Unix(nbf, 0)) || now.Add(v.skew).Before(time.Unix(iat, 0)) {
		return fail(ErrNotYetValid)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	return p, nil
}

// Check only size-bounded compact input. Re-encoding rejects ignored CR/LF and
// unused padding-bit aliases as well as padded or otherwise invalid encodings.
// Direct JWE alone permits an empty encrypted-key segment at index 1.
func canonicalSegments(parts []string, emptyIndex int) bool {
	for n, part := range parts {
		if part == "" {
			if n == emptyIndex {
				continue
			}
			return false
		}
		decoded, err := base64.RawURLEncoding.DecodeString(part)
		if err != nil || base64.RawURLEncoding.EncodeToString(decoded) != part {
			return false
		}
	}
	return true
}

// A strict allow-list blocks compression, key URLs, embedded keys, critical
// extensions and algorithm confusion before any JOSE operation. No network lookup.
func header(encoded string, expected map[string]string) (string, error) {
	if len(encoded) > 2048 {
		return "", ErrInvalid
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return "", ErrInvalid
	}
	obj, err := object(raw)
	if err != nil || len(obj) != len(expected)+1 {
		return "", ErrInvalid
	}
	for k, want := range expected {
		var got string
		if json.Unmarshal(obj[k], &got) != nil || got != want {
			return "", ErrInvalid
		}
	}
	var kid string
	if json.Unmarshal(obj["kid"], &kid) != nil || !validText(kid, 128) {
		return "", ErrInvalid
	}
	return kid, nil
}

// Reject duplicate top-level members, including escaped spellings, rather than
// relying on differing JSON parsers' first/last-member semantics.
func object(raw []byte) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	t, err := d.Token()
	if err != nil || t != json.Delim('{') {
		return nil, ErrInvalid
	}
	out := map[string]json.RawMessage{}
	for d.More() {
		t, err = d.Token()
		if err != nil {
			return nil, ErrInvalid
		}
		k, ok := t.(string)
		if !ok {
			return nil, ErrInvalid
		}
		if _, ok = out[k]; ok {
			return nil, ErrInvalid
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return nil, ErrInvalid
		}
		out[k] = value
	}
	if _, err = d.Token(); err != nil {
		return nil, ErrInvalid
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, ErrInvalid
	}
	return out, nil
}
