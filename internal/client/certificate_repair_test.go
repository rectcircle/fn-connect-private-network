package client

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func certificateFixture(t *testing.T, now time.Time, expired bool) (*x509.Certificate, *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rootTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	root, err := x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatal(err)
	}
	leafTemplate := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: []string{"nas.fnos.net"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	if expired {
		leafTemplate.NotAfter = now.Add(-time.Minute)
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, root, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(leafDER)
	if err != nil {
		t.Fatal(err)
	}
	return root, leaf
}

func TestCertificateRepairRequiresMatchingValidChain(t *testing.T) {
	now := time.Now()
	root, leaf := certificateFixture(t, now, false)
	otherRoot, _ := certificateFixture(t, now, false)
	expiredRoot, expiredLeaf := certificateFixture(t, now, true)
	unknown := func(cert *x509.Certificate) error {
		return &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{Cert: cert}, UnverifiedCertificates: []*x509.Certificate{cert}}
	}
	for _, tc := range []struct {
		name, host string
		root       *x509.Certificate
		err        error
		want       bool
	}{
		{"valid chain", "nas.fnos.net", root, unknown(leaf), true},
		{"wrong host", "other.fnos.net", root, unknown(leaf), false},
		{"different CA", "nas.fnos.net", otherRoot, unknown(leaf), false},
		{"expired", "nas.fnos.net", expiredRoot, unknown(expiredLeaf), false},
		{"network error", "nas.fnos.net", root, errors.New("network unreachable"), false},
		{"no peer certificates", "nas.fnos.net", root, &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := chainNeedsRoot(tc.err, tc.host, tc.root, now); got != tc.want {
				t.Fatalf("repair applicability=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestNativeConnectErrorDoesNotOfferUnrelatedRoot(t *testing.T) {
	_, leaf := certificateFixture(t, time.Now(), false)
	err := nativeConnectError("nas", &tls.CertificateVerificationError{
		Err: x509.UnknownAuthorityError{Cert: leaf}, UnverifiedCertificates: []*x509.Certificate{leaf},
	})
	if model.AsError(err).RemoteCode == missingISRGRootCode {
		t.Fatal("an unrelated CA must not trigger installation of ISRG Root X1")
	}
}

func TestCertificateRepairRefusesWithoutConsentOrApplicableFailure(t *testing.T) {
	for _, tc := range []struct {
		name     string
		request  certificateRepairRequest
		probeErr error
		code     model.ErrorCode
		probes   int
	}{
		{"no confirmation", certificateRepairRequest{FNID: "nas"}, nil, model.ErrorPermissionDenied, 0},
		{"invalid target", certificateRepairRequest{FNID: "https://evil.test/", Confirmed: true}, nil, model.ErrorInvalidArgument, 0},
		{"already trusted", certificateRepairRequest{FNID: "nas", Confirmed: true}, nil, model.ErrorFailedPrecondition, 1},
		{"unreachable", certificateRepairRequest{FNID: "nas", Confirmed: true}, errors.New("offline"), model.ErrorFailedPrecondition, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probes := 0
			err := repairISRGRoot(context.Background(), tc.request, func(context.Context, string) error { probes++; return tc.probeErr }, func(context.Context, []byte) error { t.Fatal("installer must not run"); return nil })
			if err == nil || model.AsError(err).Code != tc.code || probes != tc.probes {
				t.Fatalf("err=%v probes=%d", err, probes)
			}
		})
	}
}

func TestBundledISRGRootIsSelfSignedCA(t *testing.T) {
	root, err := isrgRootCertificate()
	if err != nil {
		t.Fatal(err)
	}
	if !root.IsCA || root.Subject.CommonName != "ISRG Root X1" {
		t.Fatal("unexpected bundled root")
	}
	if err := root.CheckSignatureFrom(root); err != nil {
		t.Fatal(err)
	}
}
