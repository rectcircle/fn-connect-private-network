//go:build darwin && cgo

package darwin

/*
#cgo LDFLAGS: -framework CoreFoundation -framework Security

#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
#include <stdlib.h>
#include <string.h>

static CFMutableDictionaryRef fncpn_keychain_query(
	const char *service,
	const char *account
) {
	CFStringRef service_value = CFStringCreateWithCString(
		NULL, service, kCFStringEncodingUTF8
	);
	CFStringRef account_value = CFStringCreateWithCString(
		NULL, account, kCFStringEncodingUTF8
	);
	if (service_value == NULL || account_value == NULL) {
		if (service_value != NULL) CFRelease(service_value);
		if (account_value != NULL) CFRelease(account_value);
		return NULL;
	}
	CFMutableDictionaryRef query = CFDictionaryCreateMutable(
		NULL,
		0,
		&kCFTypeDictionaryKeyCallBacks,
		&kCFTypeDictionaryValueCallBacks
	);
	if (query != NULL) {
		CFDictionarySetValue(query, kSecClass, kSecClassGenericPassword);
		CFDictionarySetValue(query, kSecAttrService, service_value);
		CFDictionarySetValue(query, kSecAttrAccount, account_value);
	}
	CFRelease(service_value);
	CFRelease(account_value);
	return query;
}

static OSStatus fncpn_keychain_get(
	const char *service,
	const char *account,
	unsigned char **output,
	long *output_length
) {
	*output = NULL;
	*output_length = 0;
	CFMutableDictionaryRef query = fncpn_keychain_query(service, account);
	if (query == NULL) return errSecAllocate;
	CFDictionarySetValue(query, kSecReturnData, kCFBooleanTrue);
	CFDictionarySetValue(query, kSecMatchLimit, kSecMatchLimitOne);

	CFTypeRef result = NULL;
	OSStatus status = SecItemCopyMatching(query, &result);
	CFRelease(query);
	if (status != errSecSuccess) return status;
	if (result == NULL || CFGetTypeID(result) != CFDataGetTypeID()) {
		if (result != NULL) CFRelease(result);
		return errSecDecode;
	}
	CFDataRef data = (CFDataRef)result;
	CFIndex length = CFDataGetLength(data);
	unsigned char *copy = malloc((size_t)(length == 0 ? 1 : length));
	if (copy == NULL) {
		CFRelease(result);
		return errSecAllocate;
	}
	if (length > 0) {
		memcpy(copy, CFDataGetBytePtr(data), (size_t)length);
	}
	*output = copy;
	*output_length = (long)length;
	CFRelease(result);
	return errSecSuccess;
}

static OSStatus fncpn_keychain_put(
	const char *service,
	const char *account,
	const unsigned char *value,
	long value_length
) {
	CFMutableDictionaryRef query = fncpn_keychain_query(service, account);
	if (query == NULL) return errSecAllocate;
	CFDataRef data = CFDataCreate(NULL, value, (CFIndex)value_length);
	if (data == NULL) {
		CFRelease(query);
		return errSecAllocate;
	}
	CFMutableDictionaryRef update = CFDictionaryCreateMutable(
		NULL,
		0,
		&kCFTypeDictionaryKeyCallBacks,
		&kCFTypeDictionaryValueCallBacks
	);
	if (update == NULL) {
		CFRelease(data);
		CFRelease(query);
		return errSecAllocate;
	}
	CFDictionarySetValue(update, kSecValueData, data);
	OSStatus status = SecItemUpdate(query, update);
	CFRelease(update);
	if (status == errSecItemNotFound) {
		CFDictionarySetValue(query, kSecValueData, data);
		CFDictionarySetValue(
			query,
			kSecAttrAccessible,
			kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
		);
		status = SecItemAdd(query, NULL);
	}
	CFRelease(data);
	CFRelease(query);
	return status;
}

static OSStatus fncpn_keychain_delete(
	const char *service,
	const char *account
) {
	CFMutableDictionaryRef query = fncpn_keychain_query(service, account);
	if (query == NULL) return errSecAllocate;
	OSStatus status = SecItemDelete(query);
	CFRelease(query);
	return status == errSecItemNotFound ? errSecSuccess : status;
}

static char *fncpn_keychain_error(OSStatus status) {
	CFStringRef message = SecCopyErrorMessageString(status, NULL);
	if (message == NULL) return NULL;
	CFIndex capacity = CFStringGetMaximumSizeForEncoding(
		CFStringGetLength(message),
		kCFStringEncodingUTF8
	) + 1;
	char *output = calloc((size_t)capacity, 1);
	if (output != NULL &&
		!CFStringGetCString(message, output, capacity, kCFStringEncodingUTF8)) {
		free(output);
		output = NULL;
	}
	CFRelease(message);
	return output;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

type KeychainStore struct{}

func NewKeychainStore() *KeychainStore {
	return &KeychainStore{}
}

func (*KeychainStore) Get(
	service string,
	account string,
) ([]byte, bool, error) {
	serviceValue := C.CString(service)
	accountValue := C.CString(account)
	defer C.free(unsafe.Pointer(serviceValue))
	defer C.free(unsafe.Pointer(accountValue))

	var output *C.uchar
	var outputLength C.long
	status := C.fncpn_keychain_get(
		serviceValue,
		accountValue,
		&output,
		&outputLength,
	)
	if status == C.errSecItemNotFound {
		return nil, false, nil
	}
	if status != C.errSecSuccess {
		return nil, false, keychainError(status)
	}
	defer C.free(unsafe.Pointer(output))
	return C.GoBytes(unsafe.Pointer(output), C.int(outputLength)), true, nil
}

func (*KeychainStore) Put(
	service string,
	account string,
	value []byte,
) error {
	if len(value) == 0 {
		return errors.New("keychain value must not be empty")
	}
	serviceValue := C.CString(service)
	accountValue := C.CString(account)
	defer C.free(unsafe.Pointer(serviceValue))
	defer C.free(unsafe.Pointer(accountValue))
	status := C.fncpn_keychain_put(
		serviceValue,
		accountValue,
		(*C.uchar)(unsafe.Pointer(&value[0])),
		C.long(len(value)),
	)
	if status != C.errSecSuccess {
		return keychainError(status)
	}
	return nil
}

func (*KeychainStore) Delete(service string, account string) error {
	serviceValue := C.CString(service)
	accountValue := C.CString(account)
	defer C.free(unsafe.Pointer(serviceValue))
	defer C.free(unsafe.Pointer(accountValue))
	status := C.fncpn_keychain_delete(serviceValue, accountValue)
	if status != C.errSecSuccess {
		return keychainError(status)
	}
	return nil
}

func keychainError(status C.OSStatus) error {
	message := C.fncpn_keychain_error(status)
	if message == nil {
		return fmt.Errorf("Keychain status %d", int(status))
	}
	defer C.free(unsafe.Pointer(message))
	return fmt.Errorf("Keychain status %d: %s", int(status), C.GoString(message))
}
