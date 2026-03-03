package controller

import (
	"done-hub/common"
	"done-hub/common/config"
	"done-hub/common/utils"
	"done-hub/model"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

const maxBatchCreateChannels = 2000

func GetChannelsList(c *gin.Context) {
	var params model.SearchChannelsParams
	if err := c.ShouldBindQuery(&params); err != nil {
		common.APIRespondWithError(c, http.StatusOK, err)
		return
	}

	channels, err := model.GetChannelsList(&params)
	if err != nil {
		common.APIRespondWithError(c, http.StatusOK, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    channels,
	})
}

func GetChannel(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	channel, err := model.GetChannelById(id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    channel,
	})
}

func AddChannel(c *gin.Context) {
	channel := model.Channel{}
	err := c.ShouldBindJSON(&channel)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	channel.CreatedTime = utils.GetTimestamp()
	keys, parseMode, err := parseBatchChannelKeys(channel.Key, channel.Type)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	if len(keys) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "渠道密钥不能为空",
		})
		return
	}
	if len(keys) > maxBatchCreateChannels {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": fmt.Sprintf("批量创建数量超过限制（最多 %d 个）", maxBatchCreateChannels),
		})
		return
	}

	baseUrls := []string{}
	if channel.BaseURL != nil && strings.TrimSpace(*channel.BaseURL) != "" {
		for _, raw := range strings.Split(strings.ReplaceAll(*channel.BaseURL, "\r\n", "\n"), "\n") {
			baseURL := strings.TrimSpace(raw)
			if baseURL != "" {
				baseUrls = append(baseUrls, baseURL)
			}
		}
	}
	channels := make([]model.Channel, 0, len(keys))
	for index, key := range keys {
		localChannel := channel
		localChannel.Key = key
		if index > 0 {
			localChannel.Name = localChannel.Name + "_" + strconv.Itoa(index+1)
		}

		if len(baseUrls) > index && baseUrls[index] != "" {
			localChannel.BaseURL = &baseUrls[index]
		} else if len(baseUrls) > 0 {
			localChannel.BaseURL = &baseUrls[0]
		}

		channels = append(channels, localChannel)
	}
	err = model.BatchInsertChannels(channels)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"count":      len(channels),
			"parse_mode": parseMode,
		},
	})
}

func parseBatchChannelKeys(raw string, channelType int) ([]string, string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, "empty", nil
	}

	// 支持直接粘贴 JSON 数组：["key1", {"access_token":"..."}]
	if keys, matched, err := parseJSONKeyArray(trimmed); matched {
		if err != nil {
			return nil, "json_array", err
		}
		return dedupeNonEmpty(keys), "json_array", nil
	}

	// OAuth 类渠道支持多段 JSON 对象粘贴（含多行格式）
	if isStructuredCredentialChannel(channelType) {
		if keys, matched, err := parseJSONObjectKeyBlocks(trimmed); matched {
			if err != nil {
				return nil, "json_blocks", err
			}
			return dedupeNonEmpty(keys), "json_blocks", nil
		}
	}

	// 回退：一行一个 key
	lines := strings.Split(strings.ReplaceAll(trimmed, "\r\n", "\n"), "\n")
	keys := make([]string, 0, len(lines))
	for _, line := range lines {
		key := strings.TrimSpace(line)
		if key != "" {
			keys = append(keys, key)
		}
	}
	return dedupeNonEmpty(keys), "line_split", nil
}

func parseJSONKeyArray(raw string) ([]string, bool, error) {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "[") {
		return nil, false, nil
	}

	var arr []any
	if err := json.Unmarshal([]byte(trimmed), &arr); err != nil {
		return nil, true, fmt.Errorf("key 不是合法的 JSON 数组: %w", err)
	}

	keys := make([]string, 0, len(arr))
	for _, item := range arr {
		switch v := item.(type) {
		case string:
			key := strings.TrimSpace(v)
			if key != "" {
				keys = append(keys, key)
			}
		default:
			b, err := json.Marshal(v)
			if err != nil {
				return nil, true, fmt.Errorf("JSON 数组中的凭证无法序列化: %w", err)
			}
			keys = append(keys, string(b))
		}
	}
	return keys, true, nil
}

func parseJSONObjectKeyBlocks(raw string) ([]string, bool, error) {
	trimmed := strings.TrimSpace(raw)
	if !strings.Contains(trimmed, "{") {
		return nil, false, nil
	}

	keys := make([]string, 0)
	start := -1
	depth := 0
	inString := false
	escape := false

	for i, r := range trimmed {
		if start < 0 {
			if r == '{' {
				start = i
				depth = 1
				continue
			}
			if r == ',' || r == ' ' || r == '\n' || r == '\r' || r == '\t' {
				continue
			}
			return nil, false, nil
		}

		if inString {
			if escape {
				escape = false
				continue
			}
			if r == '\\' {
				escape = true
				continue
			}
			if r == '"' {
				inString = false
			}
			continue
		}

		switch r {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth < 0 {
				return nil, true, errors.New("key 中 JSON 对象格式错误")
			}
			if depth == 0 {
				block := strings.TrimSpace(trimmed[start : i+1])
				var obj map[string]any
				if err := json.Unmarshal([]byte(block), &obj); err != nil {
					return nil, true, fmt.Errorf("key 中 JSON 对象解析失败: %w", err)
				}
				compactJSON, err := json.Marshal(obj)
				if err != nil {
					return nil, true, fmt.Errorf("key 中 JSON 对象序列化失败: %w", err)
				}
				keys = append(keys, string(compactJSON))
				start = -1
			}
		}
	}

	if start >= 0 || depth != 0 || inString {
		return nil, true, errors.New("key 中存在未闭合的 JSON 对象")
	}
	if len(keys) == 0 {
		return nil, false, nil
	}
	return keys, true, nil
}

func isStructuredCredentialChannel(channelType int) bool {
	switch channelType {
	case config.ChannelTypeGeminiCli, config.ChannelTypeClaudeCode, config.ChannelTypeCodex, config.ChannelTypeAntigravity:
		return true
	default:
		return false
	}
}

func dedupeNonEmpty(items []string) []string {
	if len(items) == 0 {
		return items
	}
	seen := make(map[string]struct{}, len(items))
	result := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func DeleteChannel(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	channel := model.Channel{Id: id}
	err := channel.Delete()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}

func DeleteChannelTag(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	err := model.DeleteChannelTag(id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}

func DeleteDisabledChannel(c *gin.Context) {
	rows, err := model.DeleteDisabledChannel()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    rows,
	})
}

func UpdateChannel(c *gin.Context) {
	channel := model.Channel{}
	err := c.ShouldBindJSON(&channel)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	if channel.Models == "" {
		err = channel.Update(false)
	} else {
		err = channel.Update(true)
	}
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    channel,
	})
}

func BatchUpdateChannelsAzureApi(c *gin.Context) {
	var params model.BatchChannelsParams
	err := c.ShouldBindJSON(&params)
	if err != nil {
		common.APIRespondWithError(c, http.StatusOK, err)
		return
	}

	if params.Ids == nil || len(params.Ids) == 0 {
		common.APIRespondWithError(c, http.StatusOK, errors.New("ids不能为空"))
		return
	}
	var count int64
	count, err = model.BatchUpdateChannelsAzureApi(&params)
	if err != nil {
		common.APIRespondWithError(c, http.StatusOK, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"data":    count,
		"success": true,
		"message": "更新成功",
	})
}

func BatchDelModelChannels(c *gin.Context) {
	var params model.BatchChannelsParams
	err := c.ShouldBindJSON(&params)
	if err != nil {
		common.APIRespondWithError(c, http.StatusOK, err)
		return
	}

	if params.Ids == nil || len(params.Ids) == 0 {
		common.APIRespondWithError(c, http.StatusOK, errors.New("ids不能为空"))
		return
	}

	var count int64
	count, err = model.BatchDelModelChannels(&params)
	if err != nil {
		common.APIRespondWithError(c, http.StatusOK, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"data":    count,
		"success": true,
		"message": "更新成功",
	})
}

func BatchAddUserGroupToChannels(c *gin.Context) {
	var params model.BatchChannelsParams
	err := c.ShouldBindJSON(&params)
	if err != nil {
		common.APIRespondWithError(c, http.StatusOK, err)
		return
	}

	if params.Ids == nil || len(params.Ids) == 0 {
		common.APIRespondWithError(c, http.StatusOK, errors.New("ids不能为空"))
		return
	}

	if params.Value == "" {
		common.APIRespondWithError(c, http.StatusOK, errors.New("用户分组不能为空"))
		return
	}

	var count int64
	count, err = model.BatchAddUserGroupToChannels(&params)
	if err != nil {
		common.APIRespondWithError(c, http.StatusOK, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"data":    count,
		"success": true,
		"message": "批量添加用户分组成功",
	})
}

func BatchAddModelToChannels(c *gin.Context) {
	var params model.BatchChannelsParams
	err := c.ShouldBindJSON(&params)
	if err != nil {
		common.APIRespondWithError(c, http.StatusOK, err)
		return
	}

	if params.Ids == nil || len(params.Ids) == 0 {
		common.APIRespondWithError(c, http.StatusOK, errors.New("ids不能为空"))
		return
	}

	if params.Value == "" {
		common.APIRespondWithError(c, http.StatusOK, errors.New("模型不能为空"))
		return
	}

	var count int64
	count, err = model.BatchAddModelToChannels(&params)
	if err != nil {
		common.APIRespondWithError(c, http.StatusOK, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"data":    count,
		"success": true,
		"message": "批量添加模型成功",
	})
}

func BatchDeleteChannel(c *gin.Context) {
	var params model.BatchChannelsParams
	err := c.ShouldBindJSON(&params)
	if err != nil {
		common.APIRespondWithError(c, http.StatusOK, err)
		return
	}

	if params.Ids == nil || len(params.Ids) == 0 {
		common.APIRespondWithError(c, http.StatusOK, errors.New("ids不能为空"))
		return
	}

	count, err := model.BatchDeleteChannel(params.Ids)
	if err != nil {
		common.APIRespondWithError(c, http.StatusOK, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    count,
	})
}
