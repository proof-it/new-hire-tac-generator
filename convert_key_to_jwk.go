package main

import (
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"log"
	"math/big"
	"os"
)

// JWK represents a JSON Web Key
type JWK struct {
	Kty string `json:"kty"`
	Use string `json:"use"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// JWKSet represents a JSON Web Key Set
type JWKSet struct {
	Keys []JWK `json:"keys"`
}

func main() {
	if len(os.Args) < 2 || len(os.Args) > 3 {
		log.Fatal("Usage: go run convert_key_to_jwk.go <public_key.pem> [--set]")
	}

	filename := os.Args[1]
	useSet := len(os.Args) == 3 && os.Args[2] == "--set"

	// Read the PEM file
	pemData, err := ioutil.ReadFile(filename)
	if err != nil {
		log.Fatalf("Error reading file: %v", err)
	}

	// Decode PEM
	block, _ := pem.Decode(pemData)
	if block == nil {
		log.Fatal("Failed to decode PEM block")
	}

	// Parse public key
	pubKey, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		log.Fatalf("Error parsing public key: %v", err)
	}

	rsaPubKey, ok := pubKey.(*rsa.PublicKey)
	if !ok {
		log.Fatal("Not an RSA public key")
	}

	// Generate kid (key identifier) from SHA256 hash of the modulus
	nBytes := rsaPubKey.N.Bytes()
	hash := sha256.Sum256(nBytes)
	kid := hex.EncodeToString(hash[:])[:16] // Use first 16 chars of hash

	// Convert to JWK format
	jwk := JWK{
		Kty: "RSA",
		Use: "sig",
		Kid: kid,
		N:   base64.RawURLEncoding.EncodeToString(rsaPubKey.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(rsaPubKey.E)).Bytes()),
	}

	var jsonData []byte
	var marshalErr error

	if useSet {
		// Output as JWK Set
		jwkSet := JWKSet{
			Keys: []JWK{jwk},
		}
		jsonData, marshalErr = json.MarshalIndent(jwkSet, "", "  ")
	} else {
		// Output as single JWK
		jsonData, marshalErr = json.MarshalIndent(jwk, "", "  ")
	}

	if marshalErr != nil {
		log.Fatalf("Error marshaling to JSON: %v", marshalErr)
	}

	fmt.Println(string(jsonData))
}