//go:build windows

package client

import (
	"context"
	"golang.org/x/sys/windows"
	"unsafe"
)

func installISRGRoot(ctx context.Context, der []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString("ROOT")
	if err != nil {
		return err
	}
	store, err := windows.CertOpenStore(windows.CERT_STORE_PROV_SYSTEM_W, 0, 0,
		windows.CERT_SYSTEM_STORE_CURRENT_USER, uintptr(unsafe.Pointer(name)))
	if err != nil {
		return err
	}
	defer windows.CertCloseStore(store, 0)
	cert, err := windows.CertCreateCertificateContext(windows.X509_ASN_ENCODING, &der[0], uint32(len(der)))
	if err != nil {
		return err
	}
	defer windows.CertFreeCertificateContext(cert)
	return windows.CertAddCertificateContextToStore(store, cert, windows.CERT_STORE_ADD_USE_EXISTING, nil)
}
