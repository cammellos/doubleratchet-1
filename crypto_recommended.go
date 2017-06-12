package doubleratchet

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
)

// DefaultCrypto is an implementation of Crypto with cryptographic primitives recommended
// by the Double Ratchet Algorithm specification. However, some details are different,
// see function comments for details.
type DefaultCrypto struct{}

func (c DefaultCrypto) GenerateDH() (DHPair, error) {
	var privKey [32]byte
	if _, err := io.ReadFull(rand.Reader, privKey[:]); err != nil {
		return DHPair{}, fmt.Errorf("couldn't generate privKey: %s", err)
	}
	privKey[0] &= 248
	privKey[31] &= 127
	privKey[31] |= 64

	var pubKey [32]byte
	curve25519.ScalarBaseMult(&pubKey, &privKey)
	return DHPair{
		PrivateKey: privKey,
		PublicKey:  pubKey,
	}, nil
}

func (c DefaultCrypto) DH(dhPair DHPair, dhPub [32]byte) [32]byte {
	var dhOut [32]byte
	curve25519.ScalarMult(&dhOut, &dhPair.PrivateKey, &dhPub)
	return dhOut
}

func (c DefaultCrypto) KdfRK(rk, dhOut [32]byte) (rootKey [32]byte, chainKey [32]byte, err error) {
	// TODO: Use sha512? Think about how to switch the implementation later if not.
	var (
		// TODO: Check if HKDF is set up correctly.
		r   = hkdf.New(sha256.New, dhOut[:], rk[:], []byte("rsZUpEuXUqqwXBvSy3EcievAh4cMj6QL"))
		buf = make([]byte, 64)
	)
	if _, err = io.ReadFull(r, buf); err != nil {
		err = fmt.Errorf("failed to generate keys: %s", err)
		return
	}
	copy(rootKey[:], buf[:32])
	copy(rootKey[:], buf[32:])
	return
}

func (c DefaultCrypto) KdfCK(ck [32]byte) (chainKey [32]byte, msgKey [32]byte) {
	const (
		ckInput = 15
		mkInput = 16
	)

	// TODO: Use sha512? Think about how to switch the implementation later if not.
	h := hmac.New(sha256.New, ck[:])

	// TODO: Handle error?
	h.Write([]byte{ckInput})
	copy(chainKey[:], h.Sum(nil))
	h.Reset()

	// TODO: Handle error?
	h.Write([]byte{mkInput})
	copy(msgKey[:], h.Sum(nil))

	return chainKey, msgKey
}

// Encrypt uses a slightly different approach over what is stated in the algorithm specification:
// it uses AES-256-CTR instead of AES-256-CBC for security, ciphertext length and implementation
// complexity considerations.
func (c DefaultCrypto) Encrypt(mk [32]byte, plaintext, associatedData []byte) ([]byte, error) {
	encKey, authKey, iv, err := c.deriveEncKeys(mk)
	if err != nil {
		return nil, err
	}

	ciphertext := make([]byte, aes.BlockSize+len(plaintext))
	copy(ciphertext[:len(iv)], iv[:])

	block, err := aes.NewCipher(encKey[:])
	if err != nil {
		return nil, fmt.Errorf("failed to create aes block cipher: %s", err)
	}
	stream := cipher.NewCTR(block, iv[:])
	stream.XORKeyStream(ciphertext[aes.BlockSize:], plaintext)

	return c.authCiphertext(authKey[:], ciphertext, associatedData), nil
}

func (c DefaultCrypto) Decrypt(mk [32]byte, authCiphertext, associatedData []byte) ([]byte, error) {
	var (
		l          = len(authCiphertext)
		iv         = authCiphertext[:aes.BlockSize]
		ciphertext = authCiphertext[aes.BlockSize : l-sha256.Size]
		signature  = authCiphertext[l-sha256.Size:]
	)

	// Check the signature.
	encKey, authKey, _, err := c.deriveEncKeys(mk)
	if err != nil {
		return nil, err
	}
	if s := c.authCiphertext(authKey[:], ciphertext, associatedData)[l-aes.BlockSize:]; !bytes.Equal(s, signature) {
		return nil, fmt.Errorf("invalid signature")
	}

	// Decrypt.
	block, err := aes.NewCipher(encKey[:])
	if err != nil {
		return nil, fmt.Errorf("failed to create aes block cipher: %s", err)
	}
	var (
		stream    = cipher.NewCTR(block, iv)
		plaintext = make([]byte, len(ciphertext))
	)
	stream.XORKeyStream(plaintext, ciphertext)

	return plaintext, nil
}

// deriveEncKeys derive keys for message encryption and decryption. Returns (encKey, authKey, iv, err).
func (c DefaultCrypto) deriveEncKeys(mk [32]byte) (encKey [32]byte, authKey [32]byte, iv [16]byte, err error) {
	// TODO: Think about switching to sha512
	// First, derive encryption and authentication key out of mk.
	salt := make([]byte, 32)
	var (
		// TODO: Check if HKDF is used correctly.
		r   = hkdf.New(sha256.New, mk[:], salt, []byte("pcwSByyx2CRdryCffXJwy7xgVZWtW5Sh"))
		buf = make([]byte, 80)
	)
	if _, err = io.ReadFull(r, buf); err != nil {
		err = fmt.Errorf("failed to generate encryption keys: %s", err)
		return
	}
	copy(encKey[:], buf[0:32])
	copy(authKey[:], buf[32:64])
	copy(iv[:], buf[64:80])
	return
}

func (c DefaultCrypto) authCiphertext(authKey, ciphertext, associatedData []byte) []byte {
	h := hmac.New(sha256.New, authKey)
	// TODO: Handle error?
	h.Write(associatedData)
	// TODO: Handle error?
	h.Write(ciphertext)
	return h.Sum(ciphertext)
}
