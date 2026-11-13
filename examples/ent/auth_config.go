package main

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"os"

	"github.com/maxtanforge/go-frame/auth/token"
)

// loadVerifier owns example-only configuration. The service accepts verification
// keys, never a private signing key; credential issuance belongs to the caller.
func loadVerifier(getenv func(string) string) (*token.Verifier, error) {
	issuer, audience := getenv("ENT_AUTH_ISSUER"), getenv("ENT_AUTH_AUDIENCE")
	signingID, encryptionID := getenv("ENT_AUTH_SIGNING_KEY_ID"), getenv("ENT_AUTH_ENCRYPTION_KEY_ID")
	keyFile, encoded := getenv("ENT_AUTH_PUBLIC_KEY_FILE"), getenv("ENT_AUTH_ENCRYPTION_KEY")
	if issuer == "" || audience == "" || signingID == "" || encryptionID == "" || keyFile == "" || encoded == "" {
		return nil, errors.New("ent example: ENT_AUTH issuer, audience, key IDs and keys are required")
	}
	encryptionKey, err := base64.RawStdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return nil, errors.New("ent example: invalid auth encryption key encoding")
	}
	raw, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, errors.New("ent example: cannot read auth public key file")
	}
	block, rest := pem.Decode(raw)
	if block == nil || block.Type != "PUBLIC KEY" || len(rest) != 0 {
		return nil, errors.New("ent example: auth public key must be one PKIX PUBLIC KEY PEM block")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, errors.New("ent example: invalid auth public key")
	}
	public, ok := parsed.(*ecdsa.PublicKey)
	if !ok {
		return nil, errors.New("ent example: auth public key must be P-256")
	}
	return token.NewVerifier(token.VerifierOptions{
		Issuer: issuer, Audience: audience,
		SigningKeys:    map[string]*ecdsa.PublicKey{signingID: public},
		EncryptionKeys: map[string][]byte{encryptionID: encryptionKey},
	})
}
