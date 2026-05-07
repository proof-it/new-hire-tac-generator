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

// SimpleJWK represents a minimal JSON Web Key
type SimpleJWK struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func main() {
	if len(os.Args) != 2 {
		log.Fatal("Usage: go run simple_jwk.go <public_key.pem>")
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

	// Generate kid from SHA256 hash of the modulus
	nBytes := rsaPubKey.N.Bytes()
	hash := sha256.Sum256(nBytes)
	kid := hex.EncodeToString(hash[:])[:16]

	// Convert to minimal JWK format
	jwk := SimpleJWK{
		Kty: "RSA",
		Kid: kid,
		N:   base64.RawURLEncoding.EncodeToString(rsaPubKey.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(rsaPubKey.E)).Bytes()),
	}

	// Convert to JSON
	jsonData, err := json.MarshalIndent(jwk, "", "  ")
	if err != nil {
		log.Fatalf("Error marshaling to JSON: %v", err)
	}

	fmt.Println(string(jsonData))
}