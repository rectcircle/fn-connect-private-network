package client

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

const missingISRGRootCode = "ISRG_ROOT_X1_MISSING"

// Official self-signed ISRG Root X1: https://letsencrypt.org/certs/isrgrootx1.der
// Bundling the pinned certificate avoids needing a working TLS trust store to
// download the root, and never accepts a certificate supplied by the peer.
//
//go:embed certificates/isrgrootx1.der
var isrgRootDER []byte

func isrgRootCertificate() (*x509.Certificate, error) {
	digest := sha256.Sum256(isrgRootDER)
	if hex.EncodeToString(digest[:]) != "96bcec06264976f37460779acf28c5a7cfe8a3c0aae11a8ffcee05c0bddf08c6" {
		return nil, errors.New("bundled ISRG Root X1 fingerprint mismatch")
	}
	return x509.ParseCertificate(isrgRootDER)
}

func chainNeedsRoot(err error, host string, root *x509.Certificate, now time.Time) bool {
	var unknown x509.UnknownAuthorityError
	var verification *tls.CertificateVerificationError
	if !errors.As(err, &unknown) || !errors.As(err, &verification) || len(verification.UnverifiedCertificates) == 0 {
		return false
	}
	roots := x509.NewCertPool()
	roots.AddCert(root)
	intermediates := x509.NewCertPool()
	for _, cert := range verification.UnverifiedCertificates[1:] {
		intermediates.AddCert(cert)
	}
	_, verifyErr := verification.UnverifiedCertificates[0].Verify(x509.VerifyOptions{
		DNSName: host, Roots: roots, Intermediates: intermediates, CurrentTime: now,
	})
	return verifyErr == nil
}

func nativeConnectError(fnID string, err error) error {
	root, rootErr := isrgRootCertificate()
	if rootErr == nil && chainNeedsRoot(err, fnID+".fnos.net", root, time.Now()) {
		failure := model.WrapError(model.ErrorFailedPrecondition, "ISRG Root X1 is missing from the system trust store", false, err)
		failure.RemoteCode = missingISRGRootCode
		return model.WithOperation(failure, "native_session.connect")
	}
	return nativeTransportError("connect", err)
}

type certificateRepairRequest struct {
	FNID      string `json:"fnId"`
	Confirmed bool   `json:"confirmed"`
}

func repairISRGRoot(ctx context.Context, request certificateRepairRequest,
	probe func(context.Context, string) error, install func(context.Context, []byte) error) error {
	if !request.Confirmed {
		return model.NewError(model.ErrorPermissionDenied, "certificate installation requires explicit confirmation", false)
	}
	fnID, err := normalizeFNID(request.FNID)
	if err != nil || fnID != request.FNID {
		return model.NewError(model.ErrorInvalidArgument, "invalid certificate repair FN ID", false)
	}
	root, err := isrgRootCertificate()
	if err != nil {
		return err
	}
	// Recheck immediately before changing trust. Only the fixed root may repair
	// this host's verified chain, including hostname, dates and server EKU.
	if !chainNeedsRoot(probe(ctx, fnID+".fnos.net"), fnID+".fnos.net", root, time.Now()) {
		return model.NewError(model.ErrorFailedPrecondition, "ISRG Root X1 installation is not applicable to this connection", false)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := install(ctx, isrgRootDER); err != nil {
		return model.WrapError(model.ErrorPermissionDenied, "could not install ISRG Root X1 for the current user", false, err)
	}
	// Do not report success until the ordinary system verifier accepts the host.
	if err := probe(ctx, fnID+".fnos.net"); err != nil {
		return model.WrapError(model.ErrorUnavailable, "certificate installed but fnOS connection verification still failed", false, err)
	}
	return nil
}

func probeCertificateTrust(ctx context.Context, host string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, "https://"+host+"/", nil)
	if err != nil {
		return err
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if response != nil {
		response.Body.Close()
	}
	return err
}
