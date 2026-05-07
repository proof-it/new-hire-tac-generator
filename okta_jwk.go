package main

import (
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"log"
	"math/big"
	"os"
)

// OktaJWK represents a JWK in the exact format Okta expects
type OktaJWK struct {
	Kty string `json:"kty"`
	Use string `json:"use"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func main() {
	if len(os.Args) != 2 {
		log.Fatal("Usage: go run okta_jwk.go <public_key.pem>")
	}

	filename := os.Args[1]

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

	// Generate N and E in base64url format
	n := base64.RawURLEncoding.EncodeToString(rsaPubKey.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(rsaPubKey.E)).Bytes())

	// Generate kid using JWK thumbprint method (RFC 7638)
	// Create a minimal JWK for thumbprint calculation
	thumbprintJWK := map[string]interface{}{
		"kty": "RSA",
		"n":   n,
		"e":   e,
	}

	// Convert to canonical JSON for thumbprint
	canonicalJSON, err := json.Marshal(thumbprintJWK)
	if err != nil {
		log.Fatalf("Error creating canonical JSON: %v", err)
	}

	// Generate SHA256 hash and encode as base64url
	hash := sha256.Sum256(canonicalJSON)
	kid := base64.RawURLEncoding.EncodeToString(hash[:])

	// Create the final JWK in Okta format
	jwk := OktaJWK{
		Kty: "RSA",
		Use: "sig",
		Kid: kid,
		N:   n,
		E:   e,
	}

	// Convert to formatted JSON
	jsonData, err := json.MarshalIndent(jwk, "", "    ")
	if err != nil {
		log.Fatalf("Error marshaling to JSON: %v", err)
	}

	fmt.Println(string(jsonData))
}