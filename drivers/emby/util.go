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
	"regexp"
	"strconv"
	"strings"
	"time"

	internalnet "github.com/OpenListTeam/OpenList/v4/internal/net"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	log "github.com/sirupsen/logrus"
)

var episodeCodeRegexp = regexp.MustCompile(`(?i)\bS\d{1,2}E\d{1,2}\b`)

const (
	embyPageSize       = 1000
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

func (d *Emby) login(ctx context.Context) error {
	d.authMu.Lock()
	defer d.authMu.Unlock()

	token, userID, err := d.authenticate(ctx)
	if err != nil {
		return err
	}
	d.token = token
	d.userID = userID
	return nil
}

func (d *Emby) authenticate(ctx context.Context) (string, string, error) {
	payload, err := json.Marshal(authReq{
		Username: d.Username,
		Pw:       d.Password,
	})
	if err != nil {
		return "", "", err
	}

	target := "/Users/AuthenticateByName"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.URL+target, bytes.NewReader(payload))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Emby-Authorization", `MediaBrowser Client="OpenList", Device="OpenList", DeviceId="openlist-emby", Version="1.0.0"`)

	resp, err := d.client.Do(req)
	if err != nil {
		return "", "", &embyHTTPError{action: "auth", target: target, cause: embyRequestCause(err)}
	}
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if readErr != nil {
		return "", "", &embyHTTPError{
			action:      "auth",
			target:      target,
			statusCode:  resp.StatusCode,
			contentType: contentType,
			body:        embyBodySnippet(body),
			cause:       readErr,
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", &embyHTTPError{
			action:      "auth",
			target:      target,
			statusCode:  resp.StatusCode,
			contentType: contentType,
			body:        embyBodySnippet(body),
		}
	}
	if !json.Valid(body) {
		return "", "", &embyHTTPError{
			action:      "auth",
			target:      target,
			statusCode:  resp.StatusCode,
			contentType: contentType,
			body:        embyBodySnippet(body),
			cause:       fmt.Errorf("invalid JSON response"),
		}
	}

	var data authResp
	if err := json.Unmarshal(body, &data); err != nil {
		return "", "", &embyHTTPError{
			action:      "auth",
			target:      target,
			statusCode:  resp.StatusCode,
			contentType: contentType,
			body:        embyBodySnippet(body),
			cause:       err,
		}
	}
	if strings.TrimSpace(data.AccessToken) == "" || strings.TrimSpace(data.User.ID) == "" {
		return "", "", fmt.Errorf("emby auth response missing access token or user id")
	}

	return strings.TrimSpace(data.AccessToken), strings.TrimSpace(data.User.ID), nil
}

func (d *Emby) getItems(ctx context.Context, parentID string) ([]embyItem, error) {
	_, userID := d.auth()
	items := make([]embyItem, 0)
	for startIndex := 0; ; {
		var page listResp
		query := url.Values{}
		query.Set("ParentId", parentID)
		query.Set("Recursive", "false")
		query.Set("Fields", "Path,Size,DateCreated,SeriesName,IndexNumber,ParentIndexNumber")
		query.Set("StartIndex", fmt.Sprintf("%d", startIndex))
		query.Set("Limit", fmt.Sprintf("%d", embyPageSize))
		if err := d.getJSON(ctx, "/Users/"+userID+"/Items", query, &page, "list"); err != nil {
			return nil, err
		}

		if page.TotalRecordCount == nil {
			return nil, fmt.Errorf("emby list response missing total record count at start index %d", startIndex)
		}
		totalRecordCount := *page.TotalRecordCount
		if totalRecordCount < 0 {
			return nil, fmt.Errorf("emby list response reported negative total record count %d", totalRecordCount)
		}
		if totalRecordCount < startIndex+len(page.Items) {
			return nil, fmt.Errorf("emby list response reported total record count %d below returned range ending at %d", totalRecordCount, startIndex+len(page.Items))
		}
		if len(page.Items) == 0 && startIndex < totalRecordCount {
			return nil, fmt.Errorf("emby list returned an empty page at start index %d before total count %d", startIndex, totalRecordCount)
		}
		items = append(items, page.Items...)
		startIndex += len(page.Items)
		if startIndex >= totalRecordCount || len(page.Items) == 0 {
			return items, nil
		}
	}
}

func (d *Emby) getViews(ctx context.Context) ([]embyItem, error) {
	_, userID := d.auth()
	var data listResp
	if err := d.getJSON(ctx, "/Users/"+userID+"/Views", nil, &data, "views"); err != nil {
		return nil, err
	}
	if data.TotalRecordCount == nil {
		return nil, fmt.Errorf("emby views response missing total record count")
	}
	if *data.TotalRecordCount != len(data.Items) {
		return nil, fmt.Errorf("emby views response reported total record count %d but returned %d items", *data.TotalRecordCount, len(data.Items))
	}
	return data.Items, nil
}

func (d *Emby) getItemDetail(ctx context.Context, fileID string) (*itemDetailResp, error) {
	_, userID := d.auth()
	var detail itemDetailResp
	query := url.Values{}
	query.Set("Fields", "MediaSources,MediaType,RunTimeTicks,UserData")
	if err := d.getJSON(ctx, "/Users/"+userID+"/Items/"+fileID, query, &detail, "item detail"); err != nil {
		return nil, err
	}
	return &detail, nil
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
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
			if readErr != nil {
				lastErr = &embyHTTPError{
					action:      action,
					target:      target,
					statusCode:  resp.StatusCode,
					contentType: contentType,
					body:        embyBodySnippet(body),
					cause:       readErr,
				}
			} else if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				httpErr := &embyHTTPError{
					action:      action,
					target:      target,
					statusCode:  resp.StatusCode,
					contentType: contentType,
					body:        embyBodySnippet(body),
				}
				if !embyRetryableStatus(resp.StatusCode) {
					return httpErr
				}
				lastErr = httpErr
			} else if out == nil {
				return nil
			} else if !json.Valid(body) {
				lastErr = &embyHTTPError{
					action:      action,
					target:      target,
					statusCode:  resp.StatusCode,
					contentType: contentType,
					body:        embyBodySnippet(body),
					cause:       fmt.Errorf("invalid JSON response"),
				}
			} else if err := json.Unmarshal(body, out); err != nil {
				lastErr = &embyHTTPError{
					action:      action,
					target:      target,
					statusCode:  resp.StatusCode,
					contentType: contentType,
					body:        embyBodySnippet(body),
					cause:       err,
				}
			} else {
				return nil
			}

			if attempt < embyMaxAttempts {
				delay := embyRetryDelay(attempt, resp.Header.Get("Retry-After"))
				logEmbyRetry(action, target, attempt, delay, lastErr)
				if err := waitEmbyRetry(ctx, delay); err != nil {
					return fmt.Errorf("%v; retry wait failed: %w", lastErr, err)
				}
				continue
			}
		}

		if attempt < embyMaxAttempts {
			delay := embyRetryDelay(attempt, "")
			logEmbyRetry(action, target, attempt, delay, lastErr)
			if err := waitEmbyRetry(ctx, delay); err != nil {
				return fmt.Errorf("%v; retry wait failed: %w", lastErr, err)
			}
		}
	}
	return lastErr
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

func (d *Emby) relogin(ctx context.Context, staleToken string) error {
	d.authMu.Lock()
	defer d.authMu.Unlock()
	if d.token != staleToken {
		return nil
	}

	token, userID, err := d.authenticate(ctx)
	if err != nil {
		return err
	}
	d.token = token
	d.userID = userID
	d.ApiKey = token
	d.UserID = userID
	op.MustSaveDriverStorage(d)
	return nil
}

func (d *Emby) auth() (string, string) {
	d.authMu.Lock()
	defer d.authMu.Unlock()
	return d.token, d.userID
}

func (d *Emby) setAuth(token, userID string) {
	d.authMu.Lock()
	d.token = token
	d.userID = userID
	d.authMu.Unlock()
}

func (d *Emby) saveAuth() {
	token, userID := d.auth()
	d.ApiKey = token
	d.UserID = userID
}
