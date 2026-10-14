//go:build darwin && cgo

package usage

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <Security/Security.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>
#include <string.h>

// readGenericPassword copies the password of the generic Keychain item named
// service into a malloc'd buffer the caller frees. It returns the OSStatus as
// a plain int, 0 on success, so Go compares it without cgo enum typing.
static int readGenericPassword(const char *service, void **out, size_t *outLen) {
	*out = NULL;
	*outLen = 0;
	CFStringRef svc = CFStringCreateWithCString(NULL, service, kCFStringEncodingUTF8);
	if (svc == NULL) {
		return (int)errSecParam;
	}
	const void *keys[] = { kSecClass, kSecAttrService, kSecReturnData, kSecMatchLimit };
	const void *vals[] = { kSecClassGenericPassword, svc, kCFBooleanTrue, kSecMatchLimitOne };
	CFDictionaryRef query = CFDictionaryCreate(NULL, keys, vals, 4,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFTypeRef result = NULL;
	OSStatus st = SecItemCopyMatching(query, &result);
	CFRelease(query);
	CFRelease(svc);
	if (st != errSecSuccess) {
		return (int)st;
	}
	CFDataRef data = (CFDataRef)result;
	CFIndex n = CFDataGetLength(data);
	void *buf = malloc(n > 0 ? (size_t)n : 1);
	if (buf == NULL) {
		CFRelease(result);
		return (int)errSecAllocate;
	}
	memcpy(buf, CFDataGetBytePtr(data), (size_t)n);
	CFRelease(result);
	*out = buf;
	*outLen = (size_t)n;
	return 0;
}
*/
import "C"

import (
	"fmt"
	"sync"
	"unsafe"
)

// keychainService is the Keychain item Electron's safeStorage creates for an
// app named "Claude". Every profile shares it: the Keychain is per app, not per
// --user-data-dir.
const keychainService = "Claude Safe Storage"

// The key is the same for every profile, so it is read once per run. A refusal
// is remembered for the run too: asking again on every panel open would turn
// "Deny" into a prompt that never goes away. Restarting the app asks again.
var (
	keyMu     sync.Mutex
	cachedKey []byte
	refused   error
)

// errSecUserCanceled and errSecAuthFailed are what SecItemCopyMatching returns
// when the user dismisses or denies the Keychain prompt.
const (
	errSecUserCanceled = -128
	errSecAuthFailed   = -25293
)

// The Security framework, not /usr/bin/security, so that "Always Allow" on
// macOS's prompt trusts this app alone. Granting it to the security command
// line tool would let any process on the machine read the item silently.
func newDecryptor(profilePath string) (decryptor, error) {
	keyMu.Lock()
	defer keyMu.Unlock()
	if refused != nil {
		return nil, refused
	}
	if cachedKey == nil {
		svc := C.CString(keychainService)
		defer C.free(unsafe.Pointer(svc))
		var out unsafe.Pointer
		var n C.size_t
		if st := int(C.readGenericPassword(svc, &out, &n)); st != 0 {
			err := errKey(fmt.Errorf("Keychain item %q unavailable (OSStatus %d)", keychainService, st))
			if st == errSecUserCanceled || st == errSecAuthFailed {
				refused = err
			}
			return nil, err
		}
		password := C.GoBytes(out, C.int(n))
		C.free(out)
		cachedKey = macKey(password)
		for i := range password {
			password[i] = 0
		}
	}
	key := cachedKey
	return func(blob []byte) ([]byte, error) { return decryptCBC(key, blob) }, nil
}
