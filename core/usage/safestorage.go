package usage

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Electron's safeStorage is Chromium's OSCrypt, so a value Claude Desktop
// encrypted with it has the shape Chromium gives its own secrets: a three-byte
// version prefix, then the ciphertext. What follows "v10" depends on the OS:
//
//   - macOS: AES-128-CBC, the key derived with PBKDF2-HMAC-SHA1 from the
//     password Claude keeps in the Keychain as "Claude Safe Storage", salt
//     "saltysalt", 1003 iterations, IV of sixteen spaces.
//   - Windows: AES-256-GCM, a 12-byte nonce after the prefix and the tag at the
//     end, the key stored DPAPI-wrapped in the profile's "Local State".
var v10 = []byte("v10")

// macKey derives the AES key from the Keychain password.
func macKey(password []byte) []byte {
	return pbkdf2SHA1(password, []byte("saltysalt"), 1003, 16)
}

// decryptCBC undoes the macOS scheme.
func decryptCBC(key, data []byte) ([]byte, error) {
	if !bytes.HasPrefix(data, v10) {
		return nil, errors.New("not a v10 value")
	}
	ct := data[len(v10):]
	if len(ct) == 0 || len(ct)%aes.BlockSize != 0 {
		return nil, errors.New("ciphertext is not a whole number of blocks")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	iv := bytes.Repeat([]byte{' '}, aes.BlockSize)
	out := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, ct)
	// PKCS#7. A wrong key almost always fails here, which is the only signal
	// CBC gives that the key was wrong.
	n := int(out[len(out)-1])
	if n == 0 || n > aes.BlockSize || n > len(out) {
		return nil, errors.New("bad padding (wrong key?)")
	}
	for _, b := range out[len(out)-n:] {
		if int(b) != n {
			return nil, errors.New("bad padding (wrong key?)")
		}
	}
	return out[:len(out)-n], nil
}

// decryptGCM undoes the Windows scheme.
func decryptGCM(key, data []byte) ([]byte, error) {
	if !bytes.HasPrefix(data, v10) {
		return nil, errors.New("not a v10 value")
	}
	rest := data[len(v10):]
	const nonceSize = 12
	if len(rest) < nonceSize+16 {
		return nil, errors.New("value too short")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, rest[:nonceSize], rest[nonceSize:], nil)
}

// pbkdf2SHA1 is RFC 8018 PBKDF2 with HMAC-SHA1. It is written out rather than
// imported because go.mod targets Go 1.22, before crypto/pbkdf2 joined the
// standard library, and pulling in golang.org/x/crypto for twenty lines is not
// worth the dependency.
func pbkdf2SHA1(password, salt []byte, iter, keyLen int) []byte {
	prf := hmac.New(sha1.New, password)
	hashLen := prf.Size()
	blocks := (keyLen + hashLen - 1) / hashLen
	out := make([]byte, 0, blocks*hashLen)
	var buf [4]byte
	for i := 1; i <= blocks; i++ {
		prf.Reset()
		prf.Write(salt)
		binary.BigEndian.PutUint32(buf[:], uint32(i))
		prf.Write(buf[:])
		u := prf.Sum(nil)
		t := append([]byte(nil), u...)
		for n := 1; n < iter; n++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(u[:0])
			for j := range t {
				t[j] ^= u[j]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}

// storedBlobs turns one config.json value into the encrypted byte strings it
// may hold. Claude Desktop does not document how it serialises safeStorage
// output, so every encoding Electron apps commonly use is tried: base64 or hex
// in a string, a Node Buffer serialised as {"type":"Buffer","data":[...]}, and
// an object or array of any of those. Only candidates carrying the v10 prefix
// are returned, so a plain string that happens to decode is not mistaken for a
// secret.
func storedBlobs(raw json.RawMessage) [][]byte {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	var out [][]byte
	var walk func(any, int)
	walk = func(v any, depth int) {
		if depth > 6 {
			return
		}
		switch t := v.(type) {
		case string:
			if b := decodeBlobString(t); b != nil {
				out = append(out, b)
			}
		case []any:
			if b := bytesFromNumbers(t); b != nil {
				if bytes.HasPrefix(b, v10) {
					out = append(out, b)
				}
				return
			}
			for _, e := range t {
				walk(e, depth+1)
			}
		case map[string]any:
			if t["type"] == "Buffer" {
				if arr, ok := t["data"].([]any); ok {
					if b := bytesFromNumbers(arr); b != nil && bytes.HasPrefix(b, v10) {
						out = append(out, b)
					}
					return
				}
			}
			for _, e := range t {
				walk(e, depth+1)
			}
		}
	}
	walk(v, 0)
	return out
}

func decodeBlobString(s string) []byte {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "v10") {
		return []byte(s)
	}
	for _, dec := range []func(string) ([]byte, error){
		base64.StdEncoding.DecodeString,
		base64.RawStdEncoding.DecodeString,
		base64.URLEncoding.DecodeString,
		hex.DecodeString,
	} {
		if b, err := dec(s); err == nil && bytes.HasPrefix(b, v10) {
			return b
		}
	}
	return nil
}

// bytesFromNumbers reads a JSON array of 0..255 integers as bytes, or returns
// nil when it is anything else.
func bytesFromNumbers(arr []any) []byte {
	if len(arr) == 0 {
		return nil
	}
	out := make([]byte, len(arr))
	for i, e := range arr {
		f, ok := e.(float64)
		if !ok || f < 0 || f > 255 || f != float64(int(f)) {
			return nil
		}
		out[i] = byte(f)
	}
	return out
}

// decryptor turns a v10 blob into plaintext with one profile's key.
type decryptor func(blob []byte) ([]byte, error)

// errKey wraps a failure to obtain the key, so the message says which step
// failed without ever including key material.
func errKey(err error) error {
	return fmt.Errorf("could not unlock Claude's saved sign-in: %w", err)
}
