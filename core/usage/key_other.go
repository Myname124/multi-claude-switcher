//go:build !darwin && !windows

package usage

// Claude Desktop ships for macOS and Windows only.
func newDecryptor(profilePath string) (decryptor, error) {
	return nil, ErrUnsupported
}
