package client

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
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

type DiscoveryClient struct {
	URL        string
	HTTPClient *http.Client
	Now        func() time.Time
	Nonce      func() (string, error)
}

func (c DiscoveryClient) Discover(
	ctx context.Context,
	fnID string,
) (Discovery, error) {
	fnID, err := normalizeFNID(fnID)
	if err != nil {
		return Discovery{}, err
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
		return Discovery{}, model.WrapError(
			model.ErrorUnavailable,
			"FN Connect discovery is unavailable",
			true,
			err,
		)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Discovery{}, model.NewError(
			model.ErrorUnavailable,
			fmt.Sprintf("FN Connect discovery returned HTTP %d", response.StatusCode),
			true,
		)
	}
	var envelope struct {
		Code int       `json:"code"`
		Msg  string    `json:"msg"`
		Data Discovery `json:"data"`
	}
	decoder := json.NewDecoder(response.Body)
	if err := decoder.Decode(&envelope); err != nil {
		return Discovery{}, fmt.Errorf("decode FN Connect discovery: %w", err)
	}
	if envelope.Code != 0 {
		return Discovery{}, fmt.Errorf(
			"FN Connect discovery failed with code %d: %s",
			envelope.Code,
			envelope.Msg,
		)
	}
	return envelope.Data, nil
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
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func valueOrDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
