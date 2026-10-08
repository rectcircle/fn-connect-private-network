//go:build darwin && cgo

package client

/*
#cgo LDFLAGS: -framework CoreFoundation -framework SystemConfiguration
#include <CoreFoundation/CoreFoundation.h>
#include <SystemConfiguration/SystemConfiguration.h>
#include <stdlib.h>
#include <string.h>

static char *fncpnString(CFStringRef value) {
 if (value == NULL || CFGetTypeID(value) != CFStringGetTypeID()) return strdup("");
 CFIndex size = CFStringGetMaximumSizeForEncoding(CFStringGetLength(value), kCFStringEncodingUTF8) + 1;
 char *out = malloc(size);
 if (out == NULL) return NULL;
 if (!CFStringGetCString(value, out, size, kCFStringEncodingUTF8)) out[0] = 0;
 return out;
}

static char *fncpnPrimaryInterface(void) {
 SCDynamicStoreRef store = SCDynamicStoreCreate(NULL, CFSTR("fncpn.snapshot"), NULL, NULL);
 if (store == NULL) return strdup("");
 CFDictionaryRef state = SCDynamicStoreCopyValue(store, CFSTR("State:/Network/Global/IPv4"));
 char *out = fncpnString(state != NULL && CFGetTypeID(state) == CFDictionaryGetTypeID()
  ? CFDictionaryGetValue(state, CFSTR("PrimaryInterface")) : NULL);
 if (state != NULL) CFRelease(state);
 CFRelease(store);
 return out;
}

// Physical service state excludes utun routing churn. Router and network
// signature distinguish DHCP/network changes that reuse an interface/subnet.
static char *fncpnPhysicalIdentity(const char *interfaceName) {
 SCDynamicStoreRef store = SCDynamicStoreCreate(NULL, CFSTR("fncpn.snapshot"), NULL, NULL);
 if (store == NULL) return strdup("");
 CFStringRef name = CFStringCreateWithCString(NULL, interfaceName, kCFStringEncodingUTF8);
 CFArrayRef keys = SCDynamicStoreCopyKeyList(store, CFSTR("State:/Network/Service/.+/IPv[46]"));
 CFMutableStringRef result = CFStringCreateMutable(NULL,0);
 if (keys != NULL) {
  for (CFIndex i = 0; i < CFArrayGetCount(keys); i++) {
   CFStringRef key = CFArrayGetValueAtIndex(keys,i);
   CFDictionaryRef state = SCDynamicStoreCopyValue(store,key);
   if (state != NULL && CFGetTypeID(state) == CFDictionaryGetTypeID()) {
    CFStringRef iface = CFDictionaryGetValue(state,CFSTR("InterfaceName"));
    if (iface != NULL && CFEqual(iface,name)) {
     CFStringAppend(result,key);
     const CFStringRef fields[] = { CFSTR("Router"), CFSTR("NetworkSignature") };
     for (int f = 0; f < 2; f++) {
      CFStringRef value = CFDictionaryGetValue(state,fields[f]);
      if (value != NULL && CFGetTypeID(value) == CFStringGetTypeID()) {
       CFStringAppend(result,CFSTR("=")); CFStringAppend(result,value);
      }
     }
     CFStringAppend(result,CFSTR("\n"));
    }
   }
   if (state != NULL) CFRelease(state);
  }
  CFRelease(keys);
 }
 char *out = fncpnString(result);
 CFRelease(result); CFRelease(name); CFRelease(store);
 return out;
}
*/
import "C"

import (
	"net"
	"slices"
	"strings"
	"unsafe"
)

func physicalIdentity(name string) string {
	input := C.CString(name)
	defer C.free(unsafe.Pointer(input))
	value := C.fncpnPhysicalIdentity(input)
	defer C.free(unsafe.Pointer(value))
	lines := strings.Fields(C.GoString(value))
	slices.Sort(lines)
	return strings.Join(lines, "\n")
}

func defaultPhysicalInterface() (string, error) {
	value := C.fncpnPrimaryInterface()
	defer C.free(unsafe.Pointer(value))
	name := C.GoString(value)
	if name != "" && usablePhysicalInterface(name, name) {
		return name, nil
	}
	// Full-tunnel VPNs can own the global default. Use a configured physical
	// service instead of identifying the VPN as the LAN probe interface.
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp != 0 && usablePhysicalInterface(iface.Name, "") && physicalIdentity(iface.Name) != "" {
			return iface.Name, nil
		}
	}
	return "", nil
}

func enrichPhysicalNetwork(snapshot *NetworkSnapshot) {
	for i := range snapshot.Links {
		snapshot.Links[i].Identity = physicalIdentity(snapshot.Links[i].Name)
	}
}
