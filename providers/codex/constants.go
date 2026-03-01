package codex

import "strings"

// Codex 支持的基础模型列表（与 new-api-main 保持同步）
var BaseModelList = []string{
	"gpt-5", "gpt-5-codex", "gpt-5-codex-mini",
	"gpt-5.1", "gpt-5.1-codex", "gpt-5.1-codex-max", "gpt-5.1-codex-mini",
	"gpt-5.2", "gpt-5.2-codex", "gpt-5.3-codex",
	"codex-mini-latest",
}

// reasoningEffortSuffixes 支持的推理力度后缀
var reasoningEffortSuffixes = []string{"-high", "-medium", "-low"}

// parseReasoningEffortFromModelSuffix 从模型名中解析推理力度后缀
// 例如: "gpt-5-codex-high" → effort="high", model="gpt-5-codex"
//
//	"gpt-5.1-codex-mini-low" → effort="low", model="gpt-5.1-codex-mini"
//	"gpt-5-codex" → effort="", model="gpt-5-codex" (无变化)
func parseReasoningEffortFromModelSuffix(model string) (effort string, originModel string) {
	for _, suffix := range reasoningEffortSuffixes {
		if strings.HasSuffix(model, suffix) {
			return suffix[1:], model[:len(model)-len(suffix)]
		}
	}
	return "", model
}

// normalizeCodexModelName 规范化 Codex 模型名称
// gpt-5-* 系列（除 gpt-5-codex、gpt-5-codex-mini 等 codex 系列）统一映射为基础模型
// 这是因为 Codex 后端只识别有限的模型标识符
func normalizeCodexModelName(model string) string {
	// 保留 codex 系列模型名（如 gpt-5-codex, gpt-5-codex-mini, gpt-5.1-codex 等）
	if strings.Contains(model, "-codex") || strings.Contains(model, ".codex") {
		return model
	}

	// gpt-5-xxx → gpt-5
	if strings.HasPrefix(model, "gpt-5-") {
		return "gpt-5"
	}

	// gpt-5.1-xxx → gpt-5.1
	if strings.HasPrefix(model, "gpt-5.1-") {
		return "gpt-5.1"
	}

	// gpt-5.2-xxx → gpt-5.2
	if strings.HasPrefix(model, "gpt-5.2-") {
		return "gpt-5.2"
	}

	return model
}
