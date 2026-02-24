package router

import (
	"done-hub/common/config"
	"done-hub/common/logger"
	"embed"
	"fmt"
	"net/http"
	"net/http/pprof"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

func SetRouter(router *gin.Engine, buildFS embed.FS, indexPage []byte) {
	// URL 路径归一化：将 /v1/v1/... 重写为 /v1/...
	// 兼容 Cherry Studio 等客户端将 Base URL 设为 https://host/v1 后自动拼接 /v1/chat/completions
	router.Use(urlNormalize(router))

	SetApiRouter(router)
	SetDashboardRouter(router)
	SetRelayRouter(router)
	// 初始化MCP服务器与Gin集成
	if config.MCP_ENABLE {
		logger.SysLog("Enable MCP Server")
		SetMcpRouter(router)
	}
	// 启用 pprof 调试端点
	if viper.GetBool("pprof_enabled") {
		logger.SysLog("Enable pprof debug endpoints at /debug/pprof/")
		SetPprofRouter(router)
	}
	frontendBaseUrl := viper.GetString("frontend_base_url")
	if config.IsMasterNode && frontendBaseUrl != "" {
		frontendBaseUrl = ""
		logger.SysLog("FRONTEND_BASE_URL is ignored on master node")
	}
	if frontendBaseUrl == "" {
		SetWebRouter(router, buildFS, indexPage)
	} else {
		frontendBaseUrl = strings.TrimSuffix(frontendBaseUrl, "/")
		router.NoRoute(func(c *gin.Context) {
			c.Redirect(http.StatusMovedPermanently, fmt.Sprintf("%s%s", frontendBaseUrl, c.Request.RequestURI))
		})
	}
}

// urlNormalize 返回 URL 路径归一化中间件
// 将重复的 /v1/v1/ 前缀归一化为 /v1/，兼容 Cherry Studio、NextChat 等
// 将 Base URL 配置为 https://host/v1 的客户端（客户端自动拼接 /v1/...，导致 /v1/v1/...）
func urlNormalize(engine *gin.Engine) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		// 循环处理多层重复的 /v1/v1/v1/... → /v1/...
		for strings.HasPrefix(path, "/v1/v1") {
			path = strings.TrimPrefix(path, "/v1")
		}
		if path != c.Request.URL.Path {
			c.Request.URL.Path = path
			if c.Request.URL.RawPath != "" {
				rawPath := c.Request.URL.RawPath
				for strings.HasPrefix(rawPath, "/v1/v1") {
					rawPath = strings.TrimPrefix(rawPath, "/v1")
				}
				c.Request.URL.RawPath = rawPath
			}
			// 更新 RequestURI 并重新路由
			c.Request.RequestURI = c.Request.URL.RequestURI()
			engine.HandleContext(c)
			c.Abort()
			return
		}
		c.Next()
	}
}

// SetPprofRouter 设置 pprof 调试路由
func SetPprofRouter(router *gin.Engine) {
	pprofGroup := router.Group("/debug/pprof")
	{
		pprofGroup.GET("/", gin.WrapF(pprof.Index))
		pprofGroup.GET("/cmdline", gin.WrapF(pprof.Cmdline))
		pprofGroup.GET("/profile", gin.WrapF(pprof.Profile))
		pprofGroup.GET("/symbol", gin.WrapF(pprof.Symbol))
		pprofGroup.POST("/symbol", gin.WrapF(pprof.Symbol))
		pprofGroup.GET("/trace", gin.WrapF(pprof.Trace))
		pprofGroup.GET("/allocs", gin.WrapH(pprof.Handler("allocs")))
		pprofGroup.GET("/block", gin.WrapH(pprof.Handler("block")))
		pprofGroup.GET("/goroutine", gin.WrapH(pprof.Handler("goroutine")))
		pprofGroup.GET("/heap", gin.WrapH(pprof.Handler("heap")))
		pprofGroup.GET("/mutex", gin.WrapH(pprof.Handler("mutex")))
		pprofGroup.GET("/threadcreate", gin.WrapH(pprof.Handler("threadcreate")))
	}
}
