//go:build darwin && cgo

package auth

/*
#cgo LDFLAGS: -framework CoreFoundation -framework Security
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>

static CFMutableDictionaryRef slack_agent_query(CFStringRef service, CFStringRef account) {
    CFMutableDictionaryRef query = CFDictionaryCreateMutable(kCFAllocatorDefault, 0,
        &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    if (query == NULL) return NULL;
    CFDictionarySetValue(query, kSecClass, kSecClassGenericPassword);
    CFDictionarySetValue(query, kSecAttrService, service);
    CFDictionarySetValue(query, kSecAttrAccount, account);
    CFDictionarySetValue(query, kSecUseAuthenticationUI, kSecUseAuthenticationUIFail);
    return query;
}

static OSStatus slack_agent_load(CFStringRef service, CFStringRef account, CFTypeRef *result) {
    CFMutableDictionaryRef query = slack_agent_query(service, account);
    if (query == NULL) return errSecAllocate;
    CFDictionarySetValue(query, kSecMatchLimit, kSecMatchLimitOne);
    CFDictionarySetValue(query, kSecReturnData, kCFBooleanTrue);
    OSStatus status = SecItemCopyMatching(query, result);
    CFRelease(query);
    return status;
}

static OSStatus slack_agent_save(CFStringRef service, CFStringRef account, CFDataRef data) {
    CFMutableDictionaryRef query = slack_agent_query(service, account);
    if (query == NULL) return errSecAllocate;
    const void *keys[] = { kSecValueData };
    const void *values[] = { data };
    CFDictionaryRef attrs = CFDictionaryCreate(kCFAllocatorDefault, keys, values, 1,
        &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    if (attrs == NULL) { CFRelease(query); return errSecAllocate; }
    OSStatus status = SecItemUpdate(query, attrs);
    CFRelease(attrs);
    if (status == errSecItemNotFound) {
        CFDictionarySetValue(query, kSecValueData, data);
        status = SecItemAdd(query, NULL);
    }
    CFRelease(query);
    return status;
}

static OSStatus slack_agent_delete(CFStringRef service, CFStringRef account) {
    CFMutableDictionaryRef query = slack_agent_query(service, account);
    if (query == NULL) return errSecAllocate;
    OSStatus status = SecItemDelete(query);
    CFRelease(query);
    return status;
}
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"unsafe"

	"github.com/abigotado/slack-agent-cli/internal/profile"
)

// KeychainStore persists credentials in macOS Keychain without allowing UI on reads.
type KeychainStore struct{}

func (KeychainStore) Load(ctx context.Context, name string) (Credential, error) {
	if err := ctx.Err(); err != nil {
		return Credential{}, err
	}
	if err := profile.ValidateName(name); err != nil {
		return Credential{}, err
	}
	service, releaseService, err := makeCFString(KeychainService)
	if err != nil {
		return Credential{}, err
	}
	defer releaseService()
	account, releaseAccount, err := makeCFString(name)
	if err != nil {
		return Credential{}, err
	}
	defer releaseAccount()
	var result C.CFTypeRef
	status := C.slack_agent_load(service, account, &result)
	if status != C.errSecSuccess {
		return Credential{}, translateStatus(status)
	}
	if result == 0 {
		return Credential{}, errors.New("keychain returned no data")
	}
	defer C.CFRelease(result)
	if C.CFGetTypeID(result) != C.CFDataGetTypeID() {
		return Credential{}, errors.New("keychain returned invalid data")
	}
	data := C.CFDataRef(result)
	length := C.CFDataGetLength(data)
	if length <= 0 || length > C.CFIndex(contractCredentialBound()) {
		return Credential{}, errors.New("stored credential is invalid")
	}
	pointer := C.CFDataGetBytePtr(data)
	if pointer == nil {
		return Credential{}, errors.New("stored credential is invalid")
	}
	return decodeCredential(C.GoBytes(unsafe.Pointer(pointer), C.int(length)))
}

func (KeychainStore) Save(ctx context.Context, name string, value Credential) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := profile.ValidateName(name); err != nil {
		return err
	}
	payload, err := encodeCredential(value)
	if err != nil {
		return err
	}
	service, releaseService, err := makeCFString(KeychainService)
	if err != nil {
		return err
	}
	defer releaseService()
	account, releaseAccount, err := makeCFString(name)
	if err != nil {
		return err
	}
	defer releaseAccount()
	data := C.CFDataCreate(C.kCFAllocatorDefault, (*C.UInt8)(unsafe.Pointer(unsafe.SliceData(payload))), C.CFIndex(len(payload)))
	if data == 0 {
		return errors.New("allocate Keychain data")
	}
	defer C.CFRelease(C.CFTypeRef(data))
	return translateStatus(C.slack_agent_save(service, account, data))
}

func (KeychainStore) Delete(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := profile.ValidateName(name); err != nil {
		return err
	}
	service, releaseService, err := makeCFString(KeychainService)
	if err != nil {
		return err
	}
	defer releaseService()
	account, releaseAccount, err := makeCFString(name)
	if err != nil {
		return err
	}
	defer releaseAccount()
	status := C.slack_agent_delete(service, account)
	if status == C.errSecItemNotFound {
		return nil
	}
	return translateStatus(status)
}

func makeCFString(value string) (C.CFStringRef, func(), error) {
	bytes := []byte(value)
	if len(bytes) == 0 {
		return 0, func() {}, errors.New("empty Keychain attribute")
	}
	result := C.CFStringCreateWithBytes(C.kCFAllocatorDefault, (*C.UInt8)(unsafe.Pointer(unsafe.SliceData(bytes))), C.CFIndex(len(bytes)), C.kCFStringEncodingUTF8, C.false)
	if result == 0 {
		return 0, func() {}, errors.New("allocate Keychain attribute")
	}
	return result, func() { C.CFRelease(C.CFTypeRef(result)) }, nil
}

func translateStatus(status C.OSStatus) error {
	switch status {
	case C.errSecSuccess:
		return nil
	case C.errSecItemNotFound:
		return ErrNotFound
	case C.errSecInteractionNotAllowed:
		return ErrInteractionNotAllowed
	default:
		return fmt.Errorf("keychain operation failed with status %d", int32(status))
	}
}

func contractCredentialBound() int { return 16 << 10 }
