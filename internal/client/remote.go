package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sync"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

const gatewayApplicationPath = "/app/fncpn"
const maxRemoteResponseSize = 256 * 1024

type Bootstrap struct {
	ProtocolVersion int                         `json:"protocolVersion"`
	Administrator   bool                        `json:"administrator"`
	Settings        model.ServerSettings        `json:"settings"`
	ServerAddress   string                      `json:"serverAddress"`
	DeviceCount     int                         `json:"deviceCount"`
	Network         model.ServerNetworkSnapshot `json:"network"`
}

type RemoteClient struct {
	mu         sync.Mutex
	baseURL    *url.URL
	httpClient *http.Client
	cookies    *cookieSet
	onCookies  func([]Cookie) error
}

func NewRemoteClient(
	baseURL string,
	cookies []Cookie,
	httpClient *http.Client,
	onCookies func([]Cookie) error,
) (*RemoteClient, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" {
		return nil, fmt.Errorf("invalid server URL %q", baseURL)
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	cookieSet, err := newCookieSet(cookies)
	if err != nil {
		return nil, fmt.Errorf("initialize cookie jar: %w", err)
	}
	return &RemoteClient{
		baseURL:    parsed,
		httpClient: httpClient,
		cookies:    cookieSet,
		onCookies:  onCookies,
	}, nil
}

func DefaultRemoteURL(fnID string) (string, error) {
	normalized, err := normalizeFNID(fnID)
	if err != nil {
		return "", err
	}
	return "https://" + normalized + ".fnos.net" + gatewayApplicationPath, nil
}

func (c *RemoteClient) Bootstrap(ctx context.Context) (Bootstrap, error) {
	var result Bootstrap
	err := c.call(ctx, http.MethodGet, "/api/v1/bootstrap", nil, &result)
	return result, err
}

func (c *RemoteClient) RegisterDevice(
	ctx context.Context,
	name string,
	publicKey string,
) (model.DeviceRegistration, error) {
	var result model.DeviceRegistration
	err := c.call(ctx, http.MethodPost, "/api/v1/devices", struct {
		Name      string `json:"name"`
		PublicKey string `json:"publicKey"`
	}{Name: name, PublicKey: publicKey}, &result)
	return result, err
}

func (c *RemoteClient) Configuration(
	ctx context.Context,
	deviceID string,
) (model.ClientConfiguration, error) {
	var result model.ClientConfiguration
	err := c.call(
		ctx,
		http.MethodGet,
		"/api/v1/devices/"+url.PathEscape(deviceID)+"/config",
		nil,
		&result,
	)
	return result, err
}

func (c *RemoteClient) WatchConfiguration(
	ctx context.Context,
	deviceID string,
	cursor string,
) (ConfigurationWatchResult, error) {
	requestPath := "/api/v1/devices/" + url.PathEscape(deviceID) + "/config"
	query := url.Values{"watch": []string{"1"}}
	if cursor != "" {
		query.Set("after", cursor)
	}
	response, err := c.do(
		ctx,
		http.MethodGet,
		requestPath,
		nil,
		query,
		30*time.Second,
	)
	if err != nil {
		return ConfigurationWatchResult{}, err
	}
	defer response.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxLocalConfigSize+1))
	decoder.DisallowUnknownFields()
	var result ConfigurationWatchResult
	if err := decoder.Decode(&result); err != nil {
		return ConfigurationWatchResult{}, fmt.Errorf(
			"decode server response: %w",
			err,
		)
	}
	if result.Cursor == "" {
		return ConfigurationWatchResult{}, errors.New(
			"server configuration watch cursor is required",
		)
	}
	return result, nil
}

func (c *RemoteClient) LocalProbeConfiguration(
	ctx context.Context,
	deviceID string,
) (model.LocalProbeConfiguration, error) {
	var result model.LocalProbeConfiguration
	err := c.call(
		ctx,
		http.MethodGet,
		"/api/v1/devices/"+url.PathEscape(deviceID)+"/local-probe",
		nil,
		&result,
	)
	return result, err
}

func (c *RemoteClient) call(
	ctx context.Context,
	method string,
	requestPath string,
	body any,
	result any,
) error {
	response, err := c.do(ctx, method, requestPath, body, nil, 0)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if result == nil {
		return nil
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxLocalConfigSize+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(result); err != nil {
		return fmt.Errorf("decode server response: %w", err)
	}
	return nil
}

func (c *RemoteClient) do(
	ctx context.Context,
	method string,
	requestPath string,
	body any,
	query url.Values,
	minimumTimeout time.Duration,
) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}
	target := *c.baseURL
	target.Path = path.Join(c.baseURL.Path, requestPath)
	target.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, method, target.String(), reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	c.addCookies(request)
	httpClient := c.httpClient
	if minimumTimeout > 0 &&
		httpClient.Timeout > 0 &&
		httpClient.Timeout < minimumTimeout {
		cloned := *httpClient
		cloned.Timeout = minimumTimeout
		httpClient = &cloned
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, model.NormalizeError(
			err,
			model.ErrorUnavailable,
			"server configuration is unavailable",
			true,
		)
	}
	if err := c.captureCookies(response); err != nil {
		response.Body.Close()
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		err := remoteStatusError(response)
		response.Body.Close()
		return nil, err
	}
	return response, nil
}

func (c *RemoteClient) addCookies(request *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, cookie := range c.cookies.Cookies(request.URL) {
		request.AddCookie(cookie)
	}
}

func (c *RemoteClient) captureCookies(response *http.Response) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.cookies.ApplyResponse(response) {
		return nil
	}
	if c.onCookies != nil {
		return c.onCookies(c.cookies.Records())
	}
	return nil
}

func remoteStatusError(response *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(response.Body, maxRemoteResponseSize+1))
	var envelope struct {
		Error *model.Error `json:"error"`
	}
	if json.Unmarshal(data, &envelope) == nil && envelope.Error != nil {
		return envelope.Error
	}
	switch response.StatusCode {
	case http.StatusUnauthorized:
		return model.NewError(
			model.ErrorAuthRequired,
			"FN Connect authorization is required",
			false,
		)
	case http.StatusForbidden:
		return model.NewError(
			model.ErrorPermissionDenied,
			"permission denied",
			false,
		)
	case http.StatusNotFound:
		return model.NewError(model.ErrorNotFound, "resource was not found", false)
	case http.StatusConflict:
		return model.NewError(model.ErrorConflict, "request conflicts with current state", false)
	case http.StatusPreconditionFailed:
		return model.NewError(
			model.ErrorFailedPrecondition,
			"request precondition failed",
			false,
		)
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return model.NewError(model.ErrorTimeout, "server request timed out", true)
	case http.StatusGone:
		return model.NewError(
			model.ErrorDeviceRevoked,
			"device was revoked",
			false,
		)
	case http.StatusServiceUnavailable, http.StatusBadGateway:
		return model.NewError(
			model.ErrorUnavailable,
			fmt.Sprintf("server returned HTTP %d", response.StatusCode),
			true,
		)
	default:
		return model.NewError(
			model.ErrorInternal,
			"server returned an unexpected response",
			false,
		)
	}
}
