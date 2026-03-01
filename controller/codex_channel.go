package controller

import (
	"context"
	"done-hub/common"
	"done-hub/common/config"
	"done-hub/common/logger"
	"done-hub/cron"
	"done-hub/model"
	"done-hub/providers/codex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// GetCodexChannelUsage 获取 Codex 渠道的 WHAM 用量信息
// GET /api/codex/channel/:id/usage
func GetCodexChannelUsage(c *gin.Context) {
	channelID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.APIRespondWithError(c, http.StatusOK, fmt.Errorf("invalid channel id: %w", err))
		return
	}

	ch, err := model.GetChannelById(channelID)
	if err != nil {
		common.APIRespondWithError(c, http.StatusOK, err)
		return
	}
	if ch == nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "channel not found"})
		return
	}
	if ch.Type != config.ChannelTypeCodex {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "channel type is not Codex"})
		return
	}

	rawKey := strings.TrimSpace(ch.Key)
	if rawKey == "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "channel key is empty"})
		return
	}

	creds, parseErr := codex.FromJSON(rawKey)
	if parseErr != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "解析凭证失败，请检查渠道配置"})
		return
	}

	accessToken := strings.TrimSpace(creds.AccessToken)
	accountID := strings.TrimSpace(creds.AccountID)
	if accessToken == "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "access_token is required"})
		return
	}
	if accountID == "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "account_id is required"})
		return
	}

	// 获取代理配置
	proxyURL := ""
	if ch.Proxy != nil && *ch.Proxy != "" {
		proxyURL = *ch.Proxy
	}

	// 构建 HTTP 客户端
	client := buildCodexHTTPClient(proxyURL)

	// 获取渠道 baseURL
	baseURL := "https://chatgpt.com"
	if ch.BaseURL != nil && *ch.BaseURL != "" {
		baseURL = strings.TrimRight(*ch.BaseURL, "/")
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	statusCode, body, fetchErr := fetchCodexWhamUsage(ctx, client, baseURL, accessToken, accountID)
	if fetchErr != nil {
		logger.SysError(fmt.Sprintf("Failed to fetch codex usage: %s", fetchErr.Error()))
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "获取用量信息失败，请稍后重试"})
		return
	}

	// 401/403 时尝试刷新凭证后重试
	if (statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden) &&
		strings.TrimSpace(creds.RefreshToken) != "" {

		refreshCtx, refreshCancel := context.WithTimeout(c.Request.Context(), codexCredentialRefreshTimeout)
		defer refreshCancel()

		if refreshErr := cron.RefreshCodexChannelCredentialInternal(refreshCtx, ch, creds); refreshErr == nil {
			// 使用新 token 重试
			ctx2, cancel2 := context.WithTimeout(c.Request.Context(), 15*time.Second)
			defer cancel2()
			statusCode, body, fetchErr = fetchCodexWhamUsage(ctx2, client, baseURL, creds.AccessToken, accountID)
			if fetchErr != nil {
				logger.SysError(fmt.Sprintf("Failed to fetch codex usage after refresh: %s", fetchErr.Error()))
				c.JSON(http.StatusOK, gin.H{"success": false, "message": "刷新凭证后获取用量信息仍然失败"})
				return
			}
			// 刷新成功后重载缓存
			model.ChannelGroup.Load()
		}
	}

	// 解析响应
	var payload interface{}
	if json.Unmarshal(body, &payload) != nil {
		payload = string(body)
	}

	ok := statusCode >= 200 && statusCode < 300
	resp := gin.H{
		"success":         ok,
		"message":         "",
		"upstream_status": statusCode,
		"data":            payload,
	}
	if !ok {
		resp["message"] = fmt.Sprintf("upstream status: %d", statusCode)
	}
	c.JSON(http.StatusOK, resp)
}

// RefreshCodexChannelCredential 手动刷新 Codex 渠道凭证
// POST /api/codex/channel/:id/refresh
func RefreshCodexChannelCredential(c *gin.Context) {
	channelID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.APIRespondWithError(c, http.StatusOK, fmt.Errorf("invalid channel id: %w", err))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	email, accountID, expiresAt, refreshErr := cron.RefreshCodexChannelCredentialByID(ctx, channelID)
	if refreshErr != nil {
		logger.SysError(fmt.Sprintf("Failed to refresh codex credential for channel %d: %s", channelID, refreshErr.Error()))
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "凭证刷新失败: " + refreshErr.Error(),
		})
		return
	}

	// 刷新成功后重载缓存
	model.ChannelGroup.Load()

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "凭证刷新成功",
		"data": gin.H{
			"channel_id": channelID,
			"account_id": accountID,
			"email":      email,
			"expires_at": expiresAt,
		},
	})
}

// codexCredentialRefreshTimeout 凭证刷新超时时间（用于 Usage 中的自动刷新重试）
const codexCredentialRefreshTimeout = 10 * time.Second

// fetchCodexWhamUsage 获取 Codex WHAM 用量数据
func fetchCodexWhamUsage(ctx context.Context, client *http.Client, baseURL string, accessToken string, accountID string) (int, []byte, error) {
	reqURL := strings.TrimRight(baseURL, "/") + "/backend-api/wham/usage"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return 0, nil, err
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("chatgpt-account-id", accountID)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("originator", "codex_cli_rs")
	req.Header.Set("User-Agent", "codex_cli_rs/0.38.0 (Ubuntu 22.4.0; x86_64) WindowsTerminal")

	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}

	return resp.StatusCode, body, nil
}

// buildCodexHTTPClient 构建支持代理的 HTTP 客户端
func buildCodexHTTPClient(proxyURL string) *http.Client {
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	if proxyURL != "" {
		proxyURLParsed, err := url.Parse(proxyURL)
		if err == nil {
			client.Transport = &http.Transport{
				Proxy: http.ProxyURL(proxyURLParsed),
			}
		}
	}

	return client
}
