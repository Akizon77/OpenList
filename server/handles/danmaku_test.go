package handles

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/gin-gonic/gin"
)

func TestFsDanmakuSearchRejectsDisabledFeature(t *testing.T) {
	gin.SetMode(gin.TestMode)
	op.Cache.SetSetting(conf.DanmakuEnabled, &model.SettingItem{
		Key:   conf.DanmakuEnabled,
		Value: "false",
	})
	t.Cleanup(op.Cache.ClearAll)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/fs/danmaku/search",
		strings.NewReader(`{"path":"/video.mp4"}`),
	)
	context.Request.Header.Set("Content-Type", "application/json")
	FsDanmakuSearch(context)

	var response struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Code != 403 {
		t.Fatalf("code = %d, want 403", response.Code)
	}
}

func TestFsDanmakuSearchRejectsDisabledGuest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	op.Cache.SetSetting(conf.DanmakuEnabled, &model.SettingItem{
		Key:   conf.DanmakuEnabled,
		Value: "true",
	})
	t.Cleanup(op.Cache.ClearAll)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/fs/danmaku/search",
		strings.NewReader(`{"path":"/video.mp4"}`),
	)
	context.Request.Header.Set("Content-Type", "application/json")
	guest := &model.User{
		Role:     model.GUEST,
		Disabled: true,
		BasePath: "/",
	}
	context.Request = context.Request.WithContext(
		contextWithUser(context.Request.Context(), guest),
	)
	FsDanmakuSearch(context)

	var response struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Code != 401 {
		t.Fatalf("code = %d, want 401", response.Code)
	}
}

func TestAuthorizeDanmakuPathRejectsPathTraversal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/fs/danmaku/search", nil)
	user := &model.User{
		Role:     model.GENERAL,
		BasePath: "/allowed",
	}
	context.Request = context.Request.WithContext(
		contextWithUser(context.Request.Context(), user),
	)

	if authorizeDanmakuPath(context, "../outside/video.mp4", "") {
		t.Fatal("authorizeDanmakuPath() = true, want false")
	}
	var response struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Code != 403 {
		t.Fatalf("code = %d, want 403", response.Code)
	}
}

func contextWithUser(ctx context.Context, user *model.User) context.Context {
	return context.WithValue(ctx, conf.UserKey, user)
}
