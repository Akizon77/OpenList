package emby

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	internalnet "github.com/OpenListTeam/OpenList/v4/internal/net"
	log "github.com/sirupsen/logrus"
)

const (
	embyMaxAttempts    = 4
	embyErrorBodyLimit = 512
	embyMaxRetryDelay  = 30 * time.Second
)

var embyRetryBaseDelay = time.Second

type embyHTTPError struct {
	action      string
	target      string
	statusCode  int
	contentType string
	body        string
	cause       error
}

func (e *embyHTTPError) Error() string {
	message := fmt.Sprintf("emby %s failed: target=%s", e.action, e.target)
	if e.statusCode > 0 {
		message += fmt.Sprintf(" status=%d", e.statusCode)
	}
	if e.contentType != "" {
		message += fmt.Sprintf(" content_type=%q", e.contentType)
	}
	if e.cause != nil {
		message += fmt.Sprintf(" error=%q", e.cause.Error())
	}
	if e.body != "" {
		message += fmt.Sprintf(" body=%q", e.body)
	}
	return message
}

func (e *embyHTTPError) unauthorized() bool {
	return e.statusCode == http.StatusUnauthorized
}

func (e *embyHTTPError) Unwrap() error {
	return e.cause
}

func (d *Emby) getJSON(ctx context.Context, endpoint string, query url.Values, out any, action string) error {
	return d.requestJSON(ctx, http.MethodGet, endpoint, query, nil, out, action, "openlist-emby")
}

func (d *Emby) postJSONWithDevice(ctx context.Context, endpoint string, query url.Values, payload, out any, action, deviceID string) error {
	return d.requestJSON(ctx, http.MethodPost, endpoint, query, payload, out, action, deviceID)
}

func (d *Emby) requestJSON(ctx context.Context, method, endpoint string, query url.Values, payload, out any, action, deviceID string) error {
	token, _ := d.auth()
	err := d.doJSONRequest(ctx, method, endpoint, query, payload, token, out, action, deviceID)
	if err == nil {
		return nil
	}
	requestErr, ok := err.(*embyHTTPError)
	if !ok || !requestErr.unauthorized() || strings.TrimSpace(d.Username) == "" || strings.TrimSpace(d.Password) == "" {
		return err
	}

	if err := d.relogin(ctx, token); err != nil {
		return err
	}
	newToken, _ := d.auth()
	return d.doJSONRequest(ctx, method, endpoint, query, payload, newToken, out, action, deviceID)
}

func (d *Emby) doJSONRequest(ctx context.Context, method, endpoint string, query url.Values, payload any, token string, out any, action, deviceID string) error {
	u, err := url.Parse(d.URL + endpoint)
	if err != nil {
		return err
	}
	q := u.Query()
	for key, values := range query {
		q.Del(key)
		for _, value := range values {
			q.Add(key, value)
		}
	}
	q.Set("api_key", token)
	u.RawQuery = q.Encode()
	target := endpoint
	if encodedQuery := query.Encode(); encodedQuery != "" {
		target += "?" + encodedQuery
	}
	var requestBody []byte
	if payload != nil {
		requestBody, err = json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode emby %s request: %w", action, err)
		}
	}

	var lastErr error
	for attempt := 1; attempt <= embyMaxAttempts; attempt++ {
		retryAfter := ""
		req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(requestBody))
		if err != nil {
			return err
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("X-Emby-Authorization", fmt.Sprintf(`MediaBrowser Client="OpenList", Device="OpenList Web", DeviceId="%s", Version="1.0.0"`, normalizeEmbyDeviceID(deviceID)))
		resp, err := d.client.Do(req)
		if err != nil {
			lastErr = &embyHTTPError{
				action: action,
				target: target,
				cause:  embyRequestCause(err),
			}
			if errors.Is(err, internalnet.ErrRequestRateLimitWait) {
				return lastErr
			}
			if ctx.Err() != nil {
				return fmt.Errorf("%v: %w", lastErr, ctx.Err())
			}
		} else {
			responseErr := decodeEmbyResponse(resp, out, action, target)
			if responseErr == nil {
				return nil
			}
			// Read/decode failures remain retryable even on a non-retryable status.
			if responseErr.cause == nil && !embyRetryableStatus(resp.StatusCode) {
				return responseErr
			}
			lastErr = responseErr
			retryAfter = resp.Header.Get("Retry-After")
		}

		if attempt < embyMaxAttempts {
			delay := embyRetryDelay(attempt, retryAfter)
			logEmbyRetry(action, target, attempt, delay, lastErr)
			if err := waitEmbyRetry(ctx, delay); err != nil {
				return fmt.Errorf("%v; retry wait failed: %w", lastErr, err)
			}
		}
	}
	return lastErr
}

func decodeEmbyResponse(resp *http.Response, out any, action, target string) *embyHTTPError {
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if out == nil {
			return nil
		}
		if !json.Valid(body) {
			err = fmt.Errorf("invalid JSON response")
		} else {
			err = json.Unmarshal(body, out)
		}
		if err == nil {
			return nil
		}
	}
	return &embyHTTPError{
		action:      action,
		target:      target,
		statusCode:  resp.StatusCode,
		contentType: strings.TrimSpace(resp.Header.Get("Content-Type")),
		body:        embyBodySnippet(body),
		cause:       err,
	}
}

func embyRetryableStatus(statusCode int) bool {
	return statusCode == http.StatusRequestTimeout ||
		statusCode == http.StatusTooEarly ||
		statusCode == http.StatusTooManyRequests ||
		statusCode >= http.StatusInternalServerError
}

func embyRequestCause(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return urlErr.Err
	}
	return err
}

func embyBodySnippet(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	runes := []rune(trimmed)
	if len(runes) <= embyErrorBodyLimit {
		return trimmed
	}
	return string(runes[:embyErrorBodyLimit]) + "..."
}

func embyRetryDelay(attempt int, retryAfter string) time.Duration {
	delay := embyRetryBaseDelay * time.Duration(1<<(attempt-1))
	if seconds, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && seconds >= 0 {
		serverDelay := time.Duration(seconds) * time.Second
		if serverDelay > delay {
			delay = serverDelay
		}
	} else if retryAt, err := http.ParseTime(strings.TrimSpace(retryAfter)); err == nil {
		serverDelay := time.Until(retryAt)
		if serverDelay > delay {
			delay = serverDelay
		}
	}
	if delay > embyMaxRetryDelay {
		return embyMaxRetryDelay
	}
	return delay
}

func waitEmbyRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func logEmbyRetry(action, target string, attempt int, delay time.Duration, err error) {
	log.WithError(err).Warnf("emby %s transient failure for %s (attempt %d/%d), retrying in %s", action, target, attempt, embyMaxAttempts, delay)
}
