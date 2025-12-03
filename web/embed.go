package web

import "embed"

// FS 内嵌前端静态资源：
// - 根上传页：index.html / index.css / index.js
// - 管理后台：admin 下所有文件
// - 安装向导：setup 下所有文件
//
//go:embed index.html index.css index.js admin/* setup/*
var FS embed.FS