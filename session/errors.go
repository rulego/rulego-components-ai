package session

import "errors"

var (
	// ErrSessionNotFound 会话不存在
	ErrSessionNotFound = errors.New("session not found")

	// ErrSessionAlreadyExists 会话已存在
	ErrSessionAlreadyExists = errors.New("session already exists")

	// ErrInvalidSessionKey 无效的会话键
	ErrInvalidSessionKey = errors.New("invalid session key")

	// ErrStorageClosed 存储已关闭
	ErrStorageClosed = errors.New("storage closed")
)
