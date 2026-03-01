package cron

import (
	"context"
	"done-hub/common/config"
	"done-hub/common/logger"
	"done-hub/model"
	"done-hub/providers/codex"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// codexCredentialRefreshThreshold 凭证过期时间不足此阈值时触发刷新
	codexCredentialRefreshThreshold = 24 * time.Hour
	// codexCredentialRefreshBatchSize 每批查询的渠道数量
	codexCredentialRefreshBatchSize = 200
	// codexCredentialRefreshTimeout 每次刷新操作的超时时间
	codexCredentialRefreshTimeout = 15 * time.Second
)

var codexCredentialRefreshRunning atomic.Bool

// RunCodexCredentialAutoRefresh 执行一次 Codex 凭证自动刷新检查
// 扫描所有启用的 Codex 渠道，对即将过期的凭证自动刷新
func RunCodexCredentialAutoRefresh() {
	if !codexCredentialRefreshRunning.CompareAndSwap(false, true) {
		logger.SysLog("[Codex] Credential auto-refresh already running, skipping")
		return
	}
	defer codexCredentialRefreshRunning.Store(false)

	ctx := context.Background()

	var refreshed int
	var scanned int
	var failed int

	offset := 0
	for {
		var channels []*model.Channel
		err := model.DB.
			Select("id", "name", "key", "status", "proxy").
			Where("type = ? AND status = 1", config.ChannelTypeCodex).
			Order("id asc").
			Limit(codexCredentialRefreshBatchSize).
			Offset(offset).
			Find(&channels).Error
		if err != nil {
			logger.SysError(fmt.Sprintf("[Codex] Credential auto-refresh: query channels failed: %v", err))
			return
		}
		if len(channels) == 0 {
			break
		}
		offset += codexCredentialRefreshBatchSize

		for _, ch := range channels {
			if ch == nil {
				continue
			}
			scanned++

			rawKey := strings.TrimSpace(ch.Key)
			if rawKey == "" {
				continue
			}

			// 尝试解析为 JSON 凭证
			creds, err := codex.FromJSON(rawKey)
			if err != nil {
				continue
			}

			// 没有 refresh_token 的不参与自动刷新
			if strings.TrimSpace(creds.RefreshToken) == "" {
				continue
			}

			// 检查是否需要刷新: 过期时间不足阈值
			if !creds.ExpiresAt.IsZero() && time.Until(creds.ExpiresAt) > codexCredentialRefreshThreshold {
				continue
			}

			// 执行刷新
			refreshCtx, cancel := context.WithTimeout(ctx, codexCredentialRefreshTimeout)
			err = RefreshCodexChannelCredentialInternal(refreshCtx, ch, creds)
			cancel()

			if err != nil {
				failed++
				logger.SysError(fmt.Sprintf("[Codex] Credential auto-refresh: channel_id=%d name=%s refresh failed: %v",
					ch.Id, ch.Name, err))
				continue
			}

			refreshed++
			logger.SysLog(fmt.Sprintf("[Codex] Credential auto-refresh: channel_id=%d name=%s refreshed, expires_at=%s",
				ch.Id, ch.Name, creds.ExpiresAt.Format(time.RFC3339)))
		}
	}

	// 如果有刷新成功的，重新加载渠道缓存
	if refreshed > 0 {
		func() {
			defer func() {
				if r := recover(); r != nil {
					logger.SysError(fmt.Sprintf("[Codex] Credential auto-refresh: ChannelGroup.Load panic: %v", r))
				}
			}()
			model.ChannelGroup.Load()
		}()
	}

	if scanned > 0 || refreshed > 0 || failed > 0 {
		logger.SysLog(fmt.Sprintf("[Codex] Credential auto-refresh completed: scanned=%d refreshed=%d failed=%d",
			scanned, refreshed, failed))
	}
}

// RefreshCodexChannelCredentialInternal 刷新单个渠道的 Codex 凭证（内部方法）
func RefreshCodexChannelCredentialInternal(ctx context.Context, ch *model.Channel, creds *codex.OAuth2Credentials) error {
	// 获取代理配置
	proxyURL := ""
	if ch.Proxy != nil && *ch.Proxy != "" {
		proxyURL = *ch.Proxy
	}

	// 使用默认 ClientID
	if creds.ClientID == "" {
		creds.ClientID = codex.DefaultClientID
	}

	// 刷新 token
	if err := creds.Refresh(ctx, proxyURL, 3); err != nil {
		return fmt.Errorf("token refresh failed: %w", err)
	}

	// 序列化更新后的凭证
	credentialsJSON, err := creds.ToJSON()
	if err != nil {
		return fmt.Errorf("failed to serialize credentials: %w", err)
	}

	// 更新数据库
	if err := model.UpdateChannelKey(ch.Id, credentialsJSON); err != nil {
		return fmt.Errorf("failed to update channel key: %w", err)
	}

	return nil
}

// RefreshCodexChannelCredentialByID 手动刷新指定渠道的 Codex 凭证（供控制器调用）
func RefreshCodexChannelCredentialByID(ctx context.Context, channelID int) (email string, accountID string, expiresAt string, err error) {
	ch, dbErr := model.GetChannelById(channelID)
	if dbErr != nil {
		err = dbErr
		return
	}
	if ch == nil {
		err = fmt.Errorf("channel not found")
		return
	}
	if ch.Type != config.ChannelTypeCodex {
		err = fmt.Errorf("channel type is not Codex")
		return
	}

	rawKey := strings.TrimSpace(ch.Key)
	if rawKey == "" {
		err = fmt.Errorf("channel key is empty")
		return
	}

	creds, parseErr := codex.FromJSON(rawKey)
	if parseErr != nil {
		err = fmt.Errorf("failed to parse credentials: %w", parseErr)
		return
	}

	if strings.TrimSpace(creds.RefreshToken) == "" {
		err = fmt.Errorf("refresh_token is required to refresh credential")
		return
	}

	if refreshErr := RefreshCodexChannelCredentialInternal(ctx, ch, creds); refreshErr != nil {
		err = refreshErr
		return
	}

	accountID = creds.AccountID
	expiresAt = creds.ExpiresAt.Format(time.RFC3339)

	// 尝试从 JWT 提取 email
	if creds.AccessToken != "" {
		email = extractEmailFromJWT(creds.AccessToken)
	}

	return
}

// extractEmailFromJWT 从 JWT 中提取 email 字段
func extractEmailFromJWT(accessToken string) string {
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	token, _, err := parser.ParseUnverified(accessToken, jwt.MapClaims{})
	if err != nil {
		return ""
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return ""
	}

	email, ok := claims["email"].(string)
	if !ok {
		return ""
	}

	return email
}
