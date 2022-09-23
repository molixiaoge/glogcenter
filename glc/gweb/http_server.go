package gweb

import (
	"context"
	"fmt"
	"glc/cmn"
	"glc/conf"
	"glc/ldb/storage/logdata"
	"glc/onexit"
	"glc/www/service"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
)

type IgnoreGinStdoutWritter struct{}

func (w *IgnoreGinStdoutWritter) Write(p []byte) (n int, err error) {
	return 0, nil
}

func Run() {

	gin.DisableConsoleColor()                     // 关闭Gin的日志颜色
	gin.DefaultWriter = &IgnoreGinStdoutWritter{} // 关闭Gin的默认日志输出
	gin.SetMode(gin.ReleaseMode)                  // 开启Gin的Release模式

	ginEngine := gin.Default()

	// 按配置判断启用GZIP压缩
	if conf.IsEnableWebGzip() {
		ginEngine.Use(gzip.Gzip(gzip.DefaultCompression))
	}

	// 请求路径包含system变量，以方便代理转发控制
	ginEngine.GET(conf.GetContextPath()+"/v2/log/add/:system", func(c *gin.Context) {

		req := NewHttpRequest(c)
		if conf.IsEnableSecurityKey() && req.GetHeader(conf.GetHeaderSecurityKey()) != conf.GetSecurityKey() {
			c.JSON(http.StatusForbidden, "未经授权的访问，拒绝服务")
			return
		}

		md := &logdata.LogDataModel{}
		err := c.BindJSON(md)
		if err != nil {
			c.JSON(http.StatusOK, Error500(err.Error()))
			return
		}
		md.System = c.Param("system")

		matched, _ := regexp.MatchString(`^[0-9a-zA-Z]+$`, md.System)
		if !matched {
			log.Println("无效的system名： " + md.System + "，仅支持字母数字")
			c.JSON(http.StatusBadRequest, "无效的system名： "+md.System+"，仅支持字母数字")
			return
		}

		service.AddTextLog(md)
		c.JSON(http.StatusOK, Ok())
	})

	ginEngine.NoRoute(func(c *gin.Context) {
		req := NewHttpRequest(c)

		// filter
		filters := getFilters()
		for _, fnFilter := range filters {
			rs := fnFilter(req)
			if rs != nil {
				c.JSON(200, rs) // 过滤器返回有内容时直接返回处理结果，结束
				return
			}
		}

		// 静态文件
		path := strings.ToLower(c.Request.URL.Path)
		if cmn.EndwithsRune(path, ".html") {
			path = "/**/*.html"
		} else if cmn.EndwithsRune(path, ".css") {
			path = "/**/*.css"
		} else if cmn.EndwithsRune(path, ".js") {
			path = "/**/*.js"
		} else if cmn.EndwithsRune(path, ".png") {
			path = "/**/*.png"
		} else if cmn.EndwithsRune(path, ".ico") {
			path = "/**/*.ico"
		}

		// controller
		method := strings.ToUpper(c.Request.Method)
		handle := getHttpController(method, path)
		if handle == nil {
			c.JSON(http.StatusNotFound, Error404())
			return
		}

		rs := handle.Controller(req)
		if rs != nil {
			c.JSON(http.StatusOK, rs)
		}
	})

	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", conf.GetServerPort()), // :8080
		Handler: ginEngine,
	}

	// 优雅退出
	onexit.RegisterExitHandle(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		log.Println("退出Web服务")
		if err := httpServer.Shutdown(ctx); err != nil {
			log.Println(err)
		}
	})

	// 启动Web服务
	err := httpServer.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		log.Fatalf("%s", err) // 启动失败的话打印错误信息后退出
	}
}
