package token

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/maxtanforge/go-frame/auth/principal"
)

func fixture(t *testing.T) (*Issuer, *Verifier, *time.Time) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	aes := make([]byte, 32)
	if _, err = rand.Read(aes); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1800000000, 0)
	clock := func() time.Time { return now }
	i, err := NewIssuer(IssuerOptions{Issuer: "service", Audience: "api", SigningKeyID: "sig", EncryptionKeyID: "enc", SigningKey: key, EncryptionKey: aes, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	v, err := NewVerifier(VerifierOptions{Issuer: "service", Audience: "api", SigningKeys: map[string]*ecdsa.PublicKey{"sig": &key.PublicKey}, EncryptionKeys: map[string][]byte{"enc": aes}, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	return i, v, &now
}

func TestUntrustedClaimsAndHeaders(t *testing.T) {
	i, v, now := fixture(t)
	good := map[string]any{"iss": "service", "aud": "api", "sub": "u", "name": "Alice", "jti": "id", "iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(DefaultTTL).Unix()}
	cases := []struct {
		name, key string
		value     any
	}{
		{"issuer", "iss", "other"}, {"audience", "aud", "other"}, {"audarray", "aud", []string{"api"}},
		{"subject", "sub", ""}, {"tokenid", "jti", ""}, {"longname", "name", strings.Repeat("a", 129)},
		{"fractional", "exp", float64(now.Unix()) + 0.5}, {"overflow", "exp", json.Number("9223372036854775808")},
		{"negative", "iat", -1}, {"farfuture", "exp", maxDate + 1}, {"ttl", "exp", now.Add(DefaultTTL + time.Second).Unix()},
		{"nbfafterexp", "nbf", now.Add(DefaultTTL).Unix()}, {"nbfbeforeiat", "nbf", now.Unix() - 1},
	}
	wrap := func(payload []byte) string {
		t.Helper()
		s, e := i.signer.Sign(payload)
		if e != nil {
			t.Fatal(e)
		}
		raw, e := s.CompactSerialize()
		if e != nil {
			t.Fatal(e)
		}
		j, e := i.encrypter.Encrypt([]byte(raw))
		if e != nil {
			t.Fatal(e)
		}
		raw, e = j.CompactSerialize()
		if e != nil {
			t.Fatal(e)
		}
		return raw
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := map[string]any{}
			for k, v := range good {
				c[k] = v
			}
			c[tc.key] = tc.value
			b, e := json.Marshal(c)
			if e != nil {
				t.Fatal(e)
			}
			_, e = v.Verify(context.Background(), wrap(b))
			if !errors.Is(e, ErrInvalid) {
				t.Fatalf("accepted invalid claim: %v", e)
			}
		})
	}
	for _, k := range []string{"iss", "aud", "sub", "jti", "iat", "nbf", "exp"} {
		c := map[string]any{}
		for key, val := range good {
			if key != k {
				c[key] = val
			}
		}
		b, _ := json.Marshal(c)
		if _, e := v.Verify(context.Background(), wrap(b)); !errors.Is(e, ErrInvalid) {
			t.Fatalf("accepted missing %s: %v", k, e)
		}
	}
	b, _ := json.Marshal(good)
	upper := strings.Replace(string(b), `"iss":`, `"ISS":`, 1)
	if _, e := v.Verify(context.Background(), wrap([]byte(upper))); !errors.Is(e, ErrInvalid) {
		t.Error("accepted case-insensitive registered claim")
	}
	duplicate := append([]byte(`{"iss":"service",`), b[1:]...)
	if _, e := v.Verify(context.Background(), wrap(duplicate)); !errors.Is(e, ErrInvalid) {
		t.Fatal("duplicate claims accepted")
	}
	if _, e := v.Verify(context.Background(), wrap([]byte(strings.Repeat("x", 4097)))); !errors.Is(e, ErrInvalid) {
		t.Fatal("oversized payload accepted")
	}
	for _, h := range []string{`{"alg":"dir","enc":"A256GCM","cty":"JWT","kid":"enc","zip":"DEF"}`, `{"alg":"dir","enc":"A256GCM","cty":"JWT","kid":"enc","jku":"https://invalid.local/keys"}`, `{"alg":"dir","alg":"dir","enc":"A256GCM","cty":"JWT","kid":"enc"}`, `{"alg":"dir","enc":"A128GCM","cty":"JWT","kid":"enc"}`} {
		if _, e := header(base64.RawURLEncoding.EncodeToString([]byte(h)), map[string]string{"alg": "dir", "enc": "A256GCM", "cty": "JWT"}); !errors.Is(e, ErrInvalid) {
			t.Fatalf("header accepted: %s", h)
		}
	}
}

func TestIssuerClockOutOfRange(t *testing.T) {
	i, _, now := fixture(t)
	*now = time.Unix(math.MaxInt64, 0)
	if _, err := i.Issue(context.Background(), principal.Principal{ID: "u"}); err == nil {
		t.Fatal("accepted overflowing clock")
	}
}

func TestCopiedKeysRotationAndProfiles(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	aes := make([]byte, 32)
	rand.Read(aes)
	now := time.Unix(1800000000, 0)
	clock := func() time.Time { return now }
	io := IssuerOptions{Issuer: "service", Audience: "api", SigningKeyID: "old-sign", EncryptionKeyID: "old-enc", SigningKey: key, EncryptionKey: aes, Clock: clock}
	i, e := NewIssuer(io)
	if e != nil {
		t.Fatal(e)
	}
	vo := VerifierOptions{Issuer: "service", Audience: "api", SigningKeys: map[string]*ecdsa.PublicKey{"old-sign": &key.PublicKey}, EncryptionKeys: map[string][]byte{"old-enc": aes}, Clock: clock}
	v, e := NewVerifier(vo)
	if e != nil {
		t.Fatal(e)
	}
	// Mutate the shared scalar and coordinates in place: replacing the struct
	// alone would not detect a shallow copy sharing its big.Int pointers.
	key.D.SetInt64(0)           //nolint:staticcheck // 故意修改调用方密钥材料,验证深拷贝隔离
	key.PublicKey.X.SetInt64(0) //nolint:staticcheck // 故意修改调用方密钥材料,验证深拷贝隔离
	key.PublicKey.Y.SetInt64(0) //nolint:staticcheck // 故意修改调用方密钥材料,验证深拷贝隔离
	for k := range aes {
		aes[k] = 0
	}
	delete(vo.SigningKeys, "old-sign")
	delete(vo.EncryptionKeys, "old-enc")
	tok, e := i.Issue(context.Background(), principal.Principal{ID: "u", Issuer: "ignored", TokenID: "ignored"})
	if e != nil {
		t.Fatal(e)
	}
	p, e := v.Verify(context.Background(), tok.AccessToken)
	if e != nil || p.TokenID == "ignored" {
		t.Fatalf("mutable keys or overridden identity: %v", e)
	}
	fresh, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	a2 := make([]byte, 32)
	rand.Read(a2)
	rotated, e := NewVerifier(VerifierOptions{Issuer: "service", Audience: "api", SigningKeys: map[string]*ecdsa.PublicKey{"old-sign": v.signing["old-sign"], "new-sign": &fresh.PublicKey}, EncryptionKeys: map[string][]byte{"old-enc": v.encryption["old-enc"], "new-enc": a2}, Clock: clock})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = rotated.Verify(context.Background(), tok.AccessToken); e != nil {
		t.Fatal(e)
	}
	for _, typ := range []string{"JWT", "refresh+jwt"} {
		s, e := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: fresh}, (&jose.SignerOptions{}).WithType(jose.ContentType(typ)).WithHeader("kid", "new-sign"))
		if e != nil {
			t.Fatal(e)
		}
		signed, _ := s.Sign([]byte(`{}`))
		raw, _ := signed.CompactSerialize()
		enc, _ := i.encrypter.Encrypt([]byte(raw))
		raw, _ = enc.CompactSerialize()
		if _, e = rotated.Verify(context.Background(), raw); !errors.Is(e, ErrInvalid) {
			t.Fatal("wrong type accepted")
		}
	}
}

func TestConstructorValidation(t *testing.T) {
	if _, e := NewIssuer(IssuerOptions{}); e == nil {
		t.Fatal("empty issuer accepted")
	}
	if _, e := NewVerifier(VerifierOptions{}); e == nil {
		t.Fatal("empty verifier accepted")
	}
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	base := IssuerOptions{Issuer: "s", Audience: "a", SigningKeyID: "s", EncryptionKeyID: "e", SigningKey: key, EncryptionKey: make([]byte, 32)}
	for _, modify := range []func(*IssuerOptions){func(o *IssuerOptions) { o.EncryptionKey = make([]byte, 16) }, func(o *IssuerOptions) { o.TTL = time.Nanosecond }, func(o *IssuerOptions) { o.MaxTTL = -time.Second }, func(o *IssuerOptions) { o.ClockSkew = -time.Second }, func(o *IssuerOptions) { o.SigningKey = nil }, func(o *IssuerOptions) { o.SigningKeyID = "" }} {
		o := base
		modify(&o)
		if _, e := NewIssuer(o); e == nil {
			t.Fatal("invalid options accepted")
		}
	}
	keys := map[string][]byte{}
	for n := 0; n < 17; n++ {
		keys[strings.Repeat("a", n+1)] = make([]byte, 32)
	}
	if _, e := NewVerifier(VerifierOptions{Issuer: "s", Audience: "a", SigningKeys: map[string]*ecdsa.PublicKey{"s": &key.PublicKey}, EncryptionKeys: keys}); e == nil {
		t.Fatal("unbounded keys accepted")
	}
}

func TestRejectMalformedSigningKeys(t *testing.T) {
	newKey := func() *ecdsa.PrivateKey {
		t.Helper()
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		return key
	}
	cases := map[string]func(*ecdsa.PrivateKey){
		"nil curve":   func(k *ecdsa.PrivateKey) { k.Curve = nil },
		"wrong curve": func(k *ecdsa.PrivateKey) { k.Curve = elliptic.P384() },
		"nil scalar": func(k *ecdsa.PrivateKey) {
			k.D = nil //nolint:staticcheck // 构造受支持解析器拒绝的畸形输入
		},
		"zero scalar": func(k *ecdsa.PrivateKey) {
			k.D = new(big.Int) //nolint:staticcheck // 构造受支持解析器拒绝的畸形输入
		},
		"negative scalar": func(k *ecdsa.PrivateKey) {
			k.D = big.NewInt(-1) //nolint:staticcheck // 构造受支持解析器拒绝的畸形输入
		},
		"unreduced scalar": func(k *ecdsa.PrivateKey) {
			k.D = new(big.Int).Set(elliptic.P256().Params().N) //nolint:staticcheck // 构造受支持解析器拒绝的畸形输入
		},
		"inconsistent public": func(k *ecdsa.PrivateKey) { k.PublicKey = newKey().PublicKey },
		"missing public":      func(k *ecdsa.PrivateKey) { k.PublicKey = ecdsa.PublicKey{Curve: elliptic.P256()} },
	}
	for name, modify := range cases {
		t.Run(name, func(t *testing.T) {
			key := newKey()
			modify(key)
			issuer, err := NewIssuer(IssuerOptions{Issuer: "s", Audience: "a", SigningKeyID: "s", EncryptionKeyID: "e", SigningKey: key, EncryptionKey: []byte(strings.Repeat("a", 32))})
			if issuer != nil || err == nil {
				t.Fatal("accepted malformed signing key")
			}
		})
	}
}

func TestRejectMalformedVerificationKeys(t *testing.T) {
	cases := map[string]*ecdsa.PublicKey{
		"nil":                 nil,
		"empty":               {},
		"missing coordinates": {Curve: elliptic.P256()},
		"off curve":           {Curve: elliptic.P256(), X: big.NewInt(1), Y: big.NewInt(1)},
		"nil x":               {Curve: elliptic.P256(), Y: big.NewInt(1)},
		"nil y":               {Curve: elliptic.P256(), X: big.NewInt(1)},
		"negative coordinate": {Curve: elliptic.P256(), X: big.NewInt(-1), Y: big.NewInt(1)},
	}
	wrongCurve, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cases["wrong curve"] = &wrongCurve.PublicKey
	for name, key := range cases {
		t.Run(name, func(t *testing.T) {
			verifier, err := NewVerifier(VerifierOptions{Issuer: "s", Audience: "a", SigningKeys: map[string]*ecdsa.PublicKey{"s": key}, EncryptionKeys: map[string][]byte{"e": []byte(strings.Repeat("a", 32))}})
			if verifier != nil || err == nil {
				t.Fatal("accepted malformed verification key")
			}
		})
	}
}

func TestEncryptedRoundTrip(t *testing.T) {
	i, v, _ := fixture(t)
	got, err := i.Issue(context.Background(), principal.Principal{ID: "user-1", Name: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(got.AccessToken, ".") != 4 || got.TokenType != "Bearer" {
		t.Fatalf("bad envelope: %+v", got)
	}
	p, err := v.Verify(context.Background(), got.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "user-1" || p.Name != "Alice" || p.Issuer != "service" || p.TokenID == "" {
		t.Fatalf("bad identity: %+v", p)
	}
}

func TestInvalidAndTime(t *testing.T) {
	i, v, now := fixture(t)
	tok, err := i.Issue(context.Background(), principal.Principal{ID: "u"})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"", strings.Repeat("a", 16385), tok.AccessToken + "x", "a.b.c"} {
		_, err = v.Verify(context.Background(), s)
		if !errors.Is(err, ErrInvalid) || !errors.Is(err, principal.ErrUnauthenticated) {
			t.Fatalf("bad rejection: %v", err)
		}
	}
	*now = tok.ExpiresAt.Add(30*time.Second - time.Nanosecond)
	if _, err = v.Verify(context.Background(), tok.AccessToken); err != nil {
		t.Fatalf("before expiry boundary: %v", err)
	}
	*now = tok.ExpiresAt.Add(30 * time.Second)
	_, err = v.Verify(context.Background(), tok.AccessToken)
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("expiry boundary: %v", err)
	}
	*now = tok.ExpiresAt.Add(-15*time.Minute - 31*time.Second)
	_, err = v.Verify(context.Background(), tok.AccessToken)
	if !errors.Is(err, ErrNotYetValid) {
		t.Fatalf("future: %v", err)
	}
	*now = tok.ExpiresAt.Add(-15*time.Minute - 30*time.Second)
	if _, err = v.Verify(context.Background(), tok.AccessToken); err != nil {
		t.Fatalf("activation boundary: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = v.Verify(ctx, tok.AccessToken)
	if !errors.Is(err, context.Canceled) || errors.Is(err, principal.ErrUnauthenticated) {
		t.Fatalf("context: %v", err)
	}
}

func TestInnerSignatureRequired(t *testing.T) {
	i, v, _ := fixture(t)
	tok, err := i.Issue(context.Background(), principal.Principal{ID: "u"})
	if err != nil {
		t.Fatal(err)
	}
	j, err := jose.ParseEncryptedCompact(tok.AccessToken, []jose.KeyAlgorithm{jose.DIRECT}, []jose.ContentEncryption{jose.A256GCM})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := j.Decrypt(v.encryption["enc"])
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), ".")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	parts[1] = base64.RawURLEncoding.EncodeToString([]byte(strings.Replace(string(payload), `"sub":"u"`, `"sub":"forged"`, 1)))
	enc, err := i.encrypter.Encrypt([]byte(strings.Join(parts, ".")))
	if err != nil {
		t.Fatal(err)
	}
	forged, err := enc.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = v.Verify(context.Background(), forged); !errors.Is(err, ErrInvalid) {
		t.Fatalf("forged signed payload accepted: %v", err)
	}
	// Possession of the shared encryption key must not enable HMAC signing.
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.HS256, Key: v.encryption["enc"]}, (&jose.SignerOptions{}).WithType("frame-access+jwt").WithHeader("kid", "sig"))
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	inner, err := signed.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	enc, err = i.encrypter.Encrypt([]byte(inner))
	if err != nil {
		t.Fatal(err)
	}
	forged, err = enc.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = v.Verify(context.Background(), forged); !errors.Is(err, ErrInvalid) {
		t.Fatalf("HMAC confusion accepted: %v", err)
	}
}

func TestKeyRotationAndWrongKeys(t *testing.T) {
	oldIssuer, oldVerifier, now := fixture(t)
	newSigning, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	newEncryption := make([]byte, 32)
	if _, err = rand.Read(newEncryption); err != nil {
		t.Fatal(err)
	}
	clock := func() time.Time { return *now }
	newIssuer, err := NewIssuer(IssuerOptions{Issuer: "service", Audience: "api", SigningKeyID: "new-sign", EncryptionKeyID: "new-enc", SigningKey: newSigning, EncryptionKey: newEncryption, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	oldToken, err := oldIssuer.Issue(context.Background(), principal.Principal{ID: "u"})
	if err != nil {
		t.Fatal(err)
	}
	newToken, err := newIssuer.Issue(context.Background(), principal.Principal{ID: "u"})
	if err != nil {
		t.Fatal(err)
	}
	makeVerifier := func(sign map[string]*ecdsa.PublicKey, enc map[string][]byte) *Verifier {
		t.Helper()
		v, err := NewVerifier(VerifierOptions{Issuer: "service", Audience: "api", SigningKeys: sign, EncryptionKeys: enc, Clock: clock})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	overlap := makeVerifier(map[string]*ecdsa.PublicKey{"sig": oldVerifier.signing["sig"], "new-sign": &newSigning.PublicKey}, map[string][]byte{"enc": oldVerifier.encryption["enc"], "new-enc": newEncryption})
	for _, raw := range []string{oldToken.AccessToken, newToken.AccessToken} {
		if _, err = overlap.Verify(context.Background(), raw); err != nil {
			t.Fatalf("overlap failed: %v", err)
		}
	}
	retired := makeVerifier(map[string]*ecdsa.PublicKey{"new-sign": &newSigning.PublicKey}, map[string][]byte{"new-enc": newEncryption})
	if _, err = retired.Verify(context.Background(), newToken.AccessToken); err != nil {
		t.Fatal(err)
	}
	if _, err = retired.Verify(context.Background(), oldToken.AccessToken); !errors.Is(err, ErrInvalid) {
		t.Fatalf("retired token accepted: %v", err)
	}
	cases := []struct {
		name string
		sign map[string]*ecdsa.PublicKey
		enc  map[string][]byte
	}{
		{"wrong-signing-key", map[string]*ecdsa.PublicKey{"sig": &newSigning.PublicKey}, map[string][]byte{"enc": oldVerifier.encryption["enc"]}},
		{"wrong-encryption-key", map[string]*ecdsa.PublicKey{"sig": oldVerifier.signing["sig"]}, map[string][]byte{"enc": newEncryption}},
		{"unknown-signing-kid", map[string]*ecdsa.PublicKey{"other": oldVerifier.signing["sig"]}, map[string][]byte{"enc": oldVerifier.encryption["enc"]}},
		{"unknown-encryption-kid", map[string]*ecdsa.PublicKey{"sig": oldVerifier.signing["sig"]}, map[string][]byte{"other": oldVerifier.encryption["enc"]}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := makeVerifier(tc.sign, tc.enc)
			if _, err := v.Verify(context.Background(), oldToken.AccessToken); !errors.Is(err, ErrInvalid) || !errors.Is(err, principal.ErrUnauthenticated) {
				t.Fatalf("bad key rejection: %v", err)
			}
		})
	}
}

func TestConcurrentIssuerVerifierReuse(t *testing.T) {
	i, v, _ := fixture(t)
	var workers sync.WaitGroup
	ids := make(chan string, 32)
	for n := 0; n < 32; n++ {
		workers.Go(func() {
			tok, err := i.Issue(context.Background(), principal.Principal{ID: "u"})
			if err != nil {
				t.Error(err)
				return
			}
			p, err := v.Verify(context.Background(), tok.AccessToken)
			if err != nil {
				t.Error(err)
				return
			}
			ids <- p.TokenID
		})
	}
	workers.Wait()
	close(ids)
	seen := make(map[string]bool)
	for id := range ids {
		if seen[id] {
			t.Error("duplicate token ID")
		}
		seen[id] = true
	}
	if len(seen) != 32 {
		t.Fatalf("verified %d unique tokens, want 32", len(seen))
	}
}

func TestRejectNonCanonicalCompactEncoding(t *testing.T) {
	i, v, _ := fixture(t)
	tok, err := i.Issue(context.Background(), principal.Principal{ID: "u"})
	if err != nil {
		t.Fatal(err)
	}
	assertInvalid := func(t *testing.T, raw string) {
		t.Helper()
		if _, err := v.Verify(context.Background(), raw); !errors.Is(err, ErrInvalid) {
			t.Fatalf("noncanonical token accepted: %v", err)
		}
	}
	outer := strings.Split(tok.AccessToken, ".")
	for n := range outer {
		t.Run("outer-crlf-"+string(rune('0'+n)), func(t *testing.T) {
			parts := append([]string(nil), outer...)
			parts[n] += "\r\n"
			assertInvalid(t, strings.Join(parts, "."))
		})
	}
	// A 16-byte GCM tag has four unused low bits in its final base64 digit.
	aliasLastDigit := func(s string) string {
		const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
		last := strings.IndexByte(alphabet, s[len(s)-1])
		if last < 0 || last%16 != 0 {
			t.Fatal("unexpected canonical last digit")
		}
		return s[:len(s)-1] + string(alphabet[last+1])
	}
	t.Run("outer-tag-padding-bits", func(t *testing.T) {
		parts := append([]string(nil), outer...)
		parts[4] = aliasLastDigit(parts[4])
		assertInvalid(t, strings.Join(parts, "."))
	})
	j, err := jose.ParseEncryptedCompact(tok.AccessToken, []jose.KeyAlgorithm{jose.DIRECT}, []jose.ContentEncryption{jose.A256GCM})
	if err != nil {
		t.Fatal(err)
	}
	nested, err := j.Decrypt(v.encryption["enc"])
	if err != nil {
		t.Fatal(err)
	}
	inner := strings.Split(string(nested), ".")
	wrap := func(t *testing.T, parts []string) string {
		t.Helper()
		enc, err := i.encrypter.Encrypt([]byte(strings.Join(parts, ".")))
		if err != nil {
			t.Fatal(err)
		}
		raw, err := enc.CompactSerialize()
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	for n := range inner {
		t.Run("inner-crlf-"+string(rune('0'+n)), func(t *testing.T) {
			parts := append([]string(nil), inner...)
			parts[n] += "\r\n"
			assertInvalid(t, wrap(t, parts))
		})
	}
	// An ES256 signature contains 64 bytes and likewise has four unused bits.
	t.Run("inner-signature-padding-bits", func(t *testing.T) {
		parts := append([]string(nil), inner...)
		parts[2] = aliasLastDigit(parts[2])
		assertInvalid(t, wrap(t, parts))
	})
}

func TestZeroEncryptionKeyRejected(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	// make([]byte, 32) 误传是真实事故形态：静默接受等于用全零密钥加密。
	if _, err := NewIssuer(IssuerOptions{Issuer: "s", Audience: "a", SigningKeyID: "s", EncryptionKeyID: "e", SigningKey: key, EncryptionKey: make([]byte, 32)}); err == nil {
		t.Fatal("all-zero issuer encryption key accepted")
	}
	if _, err := NewVerifier(VerifierOptions{Issuer: "s", Audience: "a", SigningKeys: map[string]*ecdsa.PublicKey{"s": &key.PublicKey}, EncryptionKeys: map[string][]byte{"e": make([]byte, 32)}}); err == nil {
		t.Fatal("all-zero verifier encryption key accepted")
	}
}
