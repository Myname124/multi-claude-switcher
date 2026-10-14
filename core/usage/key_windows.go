//go:build windows

package usage

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// newDecryptor reads the profile's AES key. Chromium keeps it in "Local State"
// as base64 of "DPAPI" followed by the key wrapped with the user's DPAPI
// credentials, so only this Windows user can unwrap it. Each profile has its
// own Local State and so its own key.
func newDecryptor(profilePath string) (decryptor, error) {
	data, err := os.ReadFile(filepath.Join(profilePath, "Local State"))
	if err != nil {
		return nil, errKey(err)
	}
	var ls struct {
		OSCrypt struct {
			EncryptedKey string `json:"encrypted_key"`
		} `json:"os_crypt"`
	}
	if err := json.Unmarshal(data, &ls); err != nil {
		return nil, errKey(fmt.Errorf("Local State is not readable: %w", err))
	}
	wrapped, err := base64.StdEncoding.DecodeString(ls.OSCrypt.EncryptedKey)
	if err != nil || !bytes.HasPrefix(wrapped, []byte("DPAPI")) {
		return nil, errKey(errors.New("Local State has no encryption key"))
	}
	key, err := dpapiUnprotect(wrapped[len("DPAPI"):])
	if err != nil {
		return nil, errKey(err)
	}
	return func(blob []byte) ([]byte, error) { return decryptGCM(key, blob) }, nil
}

func dpapiUnprotect(in []byte) ([]byte, error) {
	if len(in) == 0 {
		return nil, errors.New("empty DPAPI blob")
	}
	src := windows.DataBlob{Size: uint32(len(in)), Data: &in[0]}
	var dst windows.DataBlob
	if err := windows.CryptUnprotectData(&src, nil, nil, 0, nil, 0, &dst); err != nil {
		return nil, fmt.Errorf("DPAPI: %w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(dst.Data)))
	out := make([]byte, dst.Size)
	copy(out, unsafe.Slice(dst.Data, dst.Size))
	return out, nil
}
