// Package web 打包要发给浏览器的页面，让服务端编译成单个二进制。
package web

import _ "embed"

// SenderHTML 是发送端页面：输入配对码、开麦、把音频推给某台接收端。
//
// 这份页面由公网控制面提供，接收端不再托管它 —— 接收端不监听任何端口。
//
//go:embed index.html
var SenderHTML []byte

// MonitorHTML 是接收端本机的诊断页，只在显式开 -monitor 时才用得到。
//
//go:embed monitor.html
var MonitorHTML []byte

// AdminHTML 是控制面的管理页：登录、生成接收端 token、注销、看谁在线。
//
// 只在配置里给了 adminTokenFile 时才会被用到；没配就是 404。
//
//go:embed admin.html
var AdminHTML []byte
