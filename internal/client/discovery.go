package client

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
	wgconfig "github.com/rectcircle/fn-connect-private-network/internal/wireguard"
)

const (
	defaultDiscoveryURL = "https://fnos.net/api/v1/fn/con"
	discoveryPath       = "/api/v1/fn/con"
	discoveryPrefix     = "NDzZTVxnRKP8Z0jXg1VAMonaG8akvh"
	discoverySuffix     = "zIGtkc3dqZnJpd29qZXJqa2w7c"
)

type Discovery struct {
	IPv4             []string       `json:"ipv4"`
	IPv6             []string       `json:"ipv6"`
	PublicIPv4       []string       `json:"publicIpv4"`
	PublicIPv6       []string       `json:"publicIpv6"`
	FN               []string       `json:"fn"`
	Port             DiscoveryPorts `json:"port"`
	CheckSum         string         `json:"checkSum"`
	Version          string         `json:"ver"`
	ForbidPublicIPv6 bool           `json:"forbbidPublicIpv6"`
}

type DiscoveryPorts struct {
	HTTPS uint16 `json:"httpsPort"`
	HTTP  uint16 `json:"httpPort"`
}

func (d Discovery) DirectIPv6Candidates() []netip.Addr {
	var result []netip.Addr
	// Some NAS versions advertise global addresses in ipv6, not publicIpv6.
	for _, values := range [][]string{d.PublicIPv6, d.IPv6} {
		for _, value := range values {
			address, err := netip.ParseAddr(strings.TrimSpace(value))
			if err == nil && wgconfig.IsPublicIPv6(address) && !slices.Contains(result, address) {
				result = append(result, address)
			}
		}
	}
	return result
}

type DiscoveryClient struct {
	URL        string
	HTTPClient *http.Client
	Now        func() time.Time
	Nonce      func() (string, error)
}

func (c DiscoveryClient) Discover(
	ctx context.Context,
	fnID string,
) (discovery Discovery, failure error) {
	var httpStatus int
	defer func() {
		if failure != nil {
			typed := model.WithOperation(failure, "discovery.request")
			typed.HTTPStatus = httpStatus
			failure = typed
		}
	}()
	fnID, err := normalizeFNID(fnID)
	if err != nil {
		return Discovery{}, model.WrapError(model.ErrorInvalidArgument, "invalid FN ID", false, err)
	}
	body, err := json.Marshal(struct {
		FNID string `json:"fnId"`
	}{FNID: fnID})
	if err != nil {
		return Discovery{}, err
	}
	now := c.Now
	if now == nil {
		now = time.Now
	}
	timestamp := strconv.FormatInt(now().UnixMilli(), 10)
	nonceFunction := c.Nonce
	if nonceFunction == nil {
		nonceFunction = discoveryNonce
	}
	nonce, err := nonceFunction()
	if err != nil {
		return Discovery{}, fmt.Errorf("generate discovery nonce: %w", err)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		valueOrDefault(c.URL, defaultDiscoveryURL),
		bytes.NewReader(body),
	)
	if err != nil {
		return Discovery{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("fn-sign", discoveryFNSign(fnID, timestamp))
	request.Header.Set(
		"authx",
		"nonce="+nonce+
			"&timestamp="+timestamp+
			"&sign="+discoveryAuthSign(body, nonce, timestamp),
	)
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return Discovery{}, model.NormalizeError(
			err,
			model.ErrorUnavailable,
			"FN Connect discovery is unavailable",
			true,
		)
	}
	defer response.Body.Close()
	httpStatus = response.StatusCode
	if response.StatusCode != http.StatusOK {
		return Discovery{}, HTTPResponseError(response, "discovery.request", nil)
	}
	var envelope struct {
		Code *int       `json:"code"`
		Msg  string     `json:"msg"`
		Data *Discovery `json:"data"`
	}
	data, readErr := io.ReadAll(io.LimitReader(response.Body, maxRemoteResponseSize+1))
	if readErr == nil && len(data) > maxRemoteResponseSize {
		readErr = errors.New("discovery response exceeds size limit")
	}
	if readErr == nil {
		readErr = json.Unmarshal(data, &envelope)
	}
	if readErr != nil {
		return Discovery{}, model.NormalizeError(readErr, model.ErrorProtocol, "invalid FN Connect discovery response", false)
	}
	if envelope.Code == nil || *envelope.Code == 0 && envelope.Data == nil {
		return Discovery{}, model.NewError(model.ErrorProtocol, "FN Connect discovery response is missing code or data", false)
	}
	if *envelope.Code != 0 {
		err := model.NewError(model.ErrorDiscoveryFailed, "FN Connect discovery was rejected", false)
		err.HTTPStatus = response.StatusCode
		err.RemoteCode = strconv.Itoa(*envelope.Code)
		err.Detail = model.SafeText(envelope.Msg)
		return Discovery{}, err
	}
	return *envelope.Data, nil
}

func discoveryFNSign(fnID, timestamp string) string {
	value := sha256.Sum256(
		[]byte("trim_connect`" + fnID + "`" + timestamp + "`anna"),
	)
	return hex.EncodeToString(value[:])
}

func discoveryAuthSign(body []byte, nonce, timestamp string) string {
	bodyDigest := md5.Sum(body)
	value := discoveryPrefix + "_" +
		discoveryPath + "_" +
		nonce + "_" +
		timestamp + "_" +
		hex.EncodeToString(bodyDigest[:]) + "_" +
		discoverySuffix
	digest := md5.Sum([]byte(value))
	return hex.EncodeToString(digest[:])
}

func discoveryNonce() (string, error) {
	// FN Connect expects the browser's six-digit decimal nonce format.
	value, err := rand.Int(rand.Reader, big.NewInt(900000))
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(value.Int64()+100000, 10), nil
}

func valueOrDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
