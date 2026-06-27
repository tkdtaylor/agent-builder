package envelope

import (
	"crypto/rand"
	"errors"
	"fmt"

	"golang.org/x/crypto/nacl/box"
)

// Seal encrypts plaintext using X25519 key agreement + XSalsa20-Poly1305 AEAD
// (nacl/box). The sender uses their private key and the recipient's public key
// to derive a shared secret and encrypt. A 24-byte random nonce is generated
// and returned alongside the ciphertext.
//
// The ciphertext is never equal to the plaintext; the AEAD overhead adds 16 bytes
// of authentication tag. Returns (ciphertext, nonce, error).
func Seal(plaintext []byte, senderPriv [32]byte, recipPub [32]byte) ([]byte, [24]byte, error) {
	var nonce [24]byte

	// Generate a random 24-byte nonce.
	_, err := rand.Read(nonce[:])
	if err != nil {
		return nil, nonce, fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Seal the plaintext using nacl/box.
	ciphertext := box.Seal(nil, plaintext, &nonce, &recipPub, &senderPriv)

	return ciphertext, nonce, nil
}

// Open decrypts ciphertext sealed by nacl/box. The recipient uses their private
// key and the sender's public key (the complement of the encryption key pair).
// Returns the plaintext and nil on success, or (nil, error) if decryption fails
// (authentication tag mismatch, truncated ciphertext, etc.).
//
// On AEAD failure, the function returns nil plaintext, never partial or garbage data.
func Open(ciphertext []byte, nonce [24]byte, recipPriv [32]byte, senderPub [32]byte) ([]byte, error) {
	plaintext, ok := box.Open(nil, ciphertext, &nonce, &senderPub, &recipPriv)
	if !ok {
		return nil, errors.New("authentication failed: nacl/box decrypt returned false")
	}
	return plaintext, nil
}

// GenerateKeyPair generates an X25519 keypair suitable for use with nacl/box.
// Returns (public key [32]byte, private key [32]byte, error).
func GenerateKeyPair() ([32]byte, [32]byte, error) {
	pub, priv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		return [32]byte{}, [32]byte{}, fmt.Errorf("failed to generate X25519 keypair: %w", err)
	}
	return *pub, *priv, nil
}
