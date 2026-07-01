package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// generateEd25519KeyPair generates an Ed25519 keypair.
// Returns (public key, private key, error).
func generateEd25519KeyPair() ([]byte, []byte, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate Ed25519 key: %w", err)
	}
	return pub, priv, nil
}

// hexEncode converts bytes to hex string.
func hexEncode(b []byte) string {
	return hex.EncodeToString(b)
}

// hexDecode converts hex string to bytes.
func hexDecode(s string) ([]byte, error) {
	return hex.DecodeString(s)
}

// marshalJSON marshals a value to JSON.
func marshalJSON(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

// unmarshalJSON unmarshals JSON into a value.
func unmarshalJSON(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}
