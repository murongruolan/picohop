// Package gui 负责 ChainProxy 的图形界面。
//
// 真正的窗口实现放在 gui_windows.go（Win32 原生），
// 非 Windows 平台由 gui_other.go 提供占位实现。
//
// 本文件只保留包文档，不声明任何符号，
// 以便两个平台各自实现 Available / Run。
package gui