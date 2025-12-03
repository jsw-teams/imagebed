package web

import "embed"

// FS 是打包到 Go 二进制中的前端文件系统。
//go:embed index.html setup/* admin/*
var FS embed.FS