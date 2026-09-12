package emby

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

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
	var data authResp
	if err := decodeEmbyResponse(resp, &data, "auth", target); err != nil {
		return "", "", err
	}
	if strings.TrimSpace(data.AccessToken) == "" || strings.TrimSpace(data.User.ID) == "" {
		return "", "", fmt.Errorf("emby auth response missing access token or user id")
	}

	return strings.TrimSpace(data.AccessToken), strings.TrimSpace(data.User.ID), nil
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
