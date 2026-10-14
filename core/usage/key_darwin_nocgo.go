//go:build darwin && !cgo

package usage

// The Keychain is only reachable through cgo. Release builds of the macOS app
// always have it; this covers a CGO_ENABLED=0 build of the CLI.
func newDecryptor(profilePath string) (decryptor, error) {
	return nil, ErrUnsupported
}
