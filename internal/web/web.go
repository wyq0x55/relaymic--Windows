// Package web 打包发送端页面，让接收端编译成单个二进制。
package web

import (
	"embed"
	"io/fs"
)

//go:embed index.html
var files embed.FS

// FS 返回可直接交给 http.FileServer 的文件系统。
func FS() fs.FS { return files }

// MonitorHTML 是接收端自用的监控页。它不跟发送端页面共用一个 FileServer：
// 那个 FileServer 挂在根路径上，任何进去的文件都会被当成发送端资源暴露出去。
//
//go:embed monitor.html
var MonitorHTML []byte
