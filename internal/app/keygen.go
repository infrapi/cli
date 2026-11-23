package app

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
)

// JWK is an EC public key as a JSON Web Key, as infrapi/lib's identity
// serves it at -/jwks.json: its kid the RFC 7638 thumbprint.
type JWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
}

// JWKS is a JWK set: an ApiRegistration's identity.jwks.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// Keygen writes a new EC P-256 key to path, PKCS#8 PEM, 0600: an API's
// identity key, INFRAPI_IDENTITY_KEY_PATH. It refuses to replace a file
// unless force. It returns the public half.
func Keygen(path string, force bool) (JWKS, error) {
	if _, err := os.Stat(path); err == nil && !force {
		return JWKS{}, fmt.Errorf("%s exists: --force replaces it", path)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return JWKS{}, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return JWKS{}, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return JWKS{}, err
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		return JWKS{}, err
	}
	// WriteFile keeps the mode of a file it replaces
	if err := os.Chmod(path, 0o600); err != nil {
		return JWKS{}, err
	}
	return PublicJWKS(&key.PublicKey)
}

// PublicKeys reads the PEM private key at path, an API's identity key, and
// returns its public half.
func PublicKeys(path string) (JWKS, error) {
	data, err := os.ReadFile(path) //#nosec G304 -- the key file the user names
	if err != nil {
		return JWKS{}, err
	}
	b, _ := pem.Decode(data)
	if b == nil {
		return JWKS{}, fmt.Errorf("%s: no PEM key", path)
	}
	var priv any
	switch b.Type {
	case "PRIVATE KEY":
		priv, err = x509.ParsePKCS8PrivateKey(b.Bytes)
	case "EC PRIVATE KEY":
		priv, err = x509.ParseECPrivateKey(b.Bytes)
	default:
		return JWKS{}, fmt.Errorf("%s: a %s: an EC P-256 private key", path, b.Type)
	}
	if err != nil {
		return JWKS{}, fmt.Errorf("%s: %w", path, err)
	}
	key, ok := priv.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() {
		return JWKS{}, fmt.Errorf("%s: an EC P-256 key only", path)
	}
	return PublicJWKS(&key.PublicKey)
}

// PublicJWKS is pub as a JWK set of one key.
func PublicJWKS(pub *ecdsa.PublicKey) (JWKS, error) {
	raw, err := pub.Bytes() // 0x04 || X || Y
	if err != nil {
		return JWKS{}, err
	}
	b64 := base64.RawURLEncoding.EncodeToString
	j := JWK{Kty: "EC", Crv: "P-256", X: b64(raw[1:33]), Y: b64(raw[33:65]), Use: "sig", Alg: "ES256"}
	thumb, _ := json.Marshal(struct {
		Crv string `json:"crv"`
		Kty string `json:"kty"`
		X   string `json:"x"`
		Y   string `json:"y"`
	}{j.Crv, j.Kty, j.X, j.Y})
	sum := sha256.Sum256(thumb)
	j.Kid = b64(sum[:])
	return JWKS{Keys: []JWK{j}}, nil
}
