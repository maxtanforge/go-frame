# Encrypted access tokens

`auth/token` uses a fixed signed-then-encrypted profile: a compact ES256 JWS with
`typ: frame-access+jwt`, inside a compact `dir` / `A256GCM` JWE with `cty: JWT`.
It deliberately does not accept arbitrary JWTs, refresh tokens, unsigned tokens,
compression, key URLs, embedded keys, or JOSE extensions.
Cryptographic processing uses [go-jose v4](https://github.com/go-jose/go-jose).

```go
// Load keys from your secret store. Never hard-code or log them.
issuer, err := token.NewIssuer(token.IssuerOptions{
    Issuer: "accounts", Audience: "orders-api",
    SigningKeyID: "sign-2026-09", SigningKey: signingPrivateKey, // P-256
    EncryptionKeyID: "enc-2026-09", EncryptionKey: encryptionKey, // 32 bytes
})
if err != nil { return err }

verifier, err := token.NewVerifier(token.VerifierOptions{
    Issuer: "accounts", Audience: "orders-api",
    SigningKeys: map[string]*ecdsa.PublicKey{
        "sign-2026-09": &signingPrivateKey.PublicKey,
    },
    EncryptionKeys: map[string][]byte{"enc-2026-09": encryptionKey},
})
if err != nil { return err }

// Invoke only after authenticating the account through your application's login.
result, err := issuer.Issue(ctx, principal.Principal{ID: "user-42", Name: "Alice"})
if err != nil { return err }
identity, err := verifier.Verify(ctx, result.AccessToken)
```

`*Verifier` implements `auth.Verifier`. Supply it to the HTTP authentication
middleware; the token package does not read headers or write HTTP responses.
`Result` contains `AccessToken`, `TokenType` (`Bearer`), and UTC `ExpiresAt`.
Treat the complete result as a credential and exclude it from logs and audits.

Only the caller's principal ID and name are used during issuance. The issuer
sets `iss`, `aud`, `jti` (24 random bytes), `iat`, `nbf`, and `exp`. Verification
requires all seven registered claims, exact issuer/audience strings, nonempty
subject/token ID, and validated bounded identity strings. Names are optional
snapshots, not authoritative live profile data or authorization information.
Audience arrays are intentionally unsupported.

`TTL` and `MaxTTL` default to 15 minutes; configure a larger `MaxTTL` explicitly
on both sides if needed. Durations must be positive whole seconds. Dates are
integer Unix seconds between 1970 and year 9999; fractional and overflowing
NumericDates are rejected. Issuance truncates the clock to seconds. `nbf` must
be at or after `iat` and before `exp`; total lifetime cannot exceed `MaxTTL`.
`ClockSkew` defaults to 30 seconds, is limited to five minutes, and must be a
nonnegative whole-second duration. Zero selects the default, not zero tolerance.
Expiration is rejected at exactly `exp + skew`; activation is allowed at
exactly `nbf - skew`. The same skew widens both ends, so the effective
acceptance window is up to `TTL + 2 × ClockSkew` (16 minutes with defaults);
plan revocation and rotation timelines accordingly. Inject `Clock` for
deterministic tests; callbacks must be safe for concurrent use.

Both constructors validate and deep-copy all keys. All-zero encryption keys
(the accidental `make([]byte, 32)`) are rejected. Signing keys must use the
standard P-256 curve; malformed keys and inconsistent private/public pairs
are rejected. Keys are copied through standard ECDSA encoding and parsing APIs.
An issuer holds one signing
private key and one encryption key. A verifier holds only public signing keys
and encryption keys, with 1–16 entries per set and independent `kid` namespaces.
For rotation, construct a new issuer with new key IDs and a verifier containing
both generations. Retain old verification/decryption keys through the last old
token's expiration plus skew. Publish the new immutable objects atomically in
your application. Never reuse an encryption key as signing material.

Tokens are limited to 16 KiB, authenticated claims to 4 KiB, and each protected
header to 2048 encoded bytes. Duplicate JSON members, unknown claim names,
case aliases, and all unexpected header fields are rejected. Key resolution is
exclusively local and never fetches URLs.
Both compact layers require canonical unpadded Base64URL: whitespace and unused
padding-bit aliases are rejected. Only the direct JWE encrypted-key segment is
empty.

`ErrInvalid`, `ErrExpired`, and `ErrNotYetValid` support `errors.Is` and each wraps
`principal.ErrUnauthenticated`. They expose no credential or JOSE diagnostic.
Cancellation and internal issuance failures remain operational errors. No replay
cache, revocation, refresh protocol, authorization policy, or account lookup is
implicit; add explicit session validation when your application needs it.
