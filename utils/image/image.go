/*
 * Copyright 2024 The RuleGo Authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Package image provides image utilities for AI components.
package image

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/image/draw"
)

// DefaultImageMaxSize 默认图片最大尺寸
const DefaultImageMaxSize = 1024

// DefaultJPEGQuality 默认 JPEG 压缩质量
const DefaultJPEGQuality = 92

// CompressOptions 图片压缩选项
type CompressOptions struct {
	// MaxSize 图片最大尺寸（宽或高的最大值），超过则等比缩放
	MaxSize int
	// Quality JPEG 压缩质量 (1-100)，值越高画质越好但文件越大
	// 推荐：颜色敏感场景使用 95+，普通场景使用 85-92
	Quality int
	// KeepFormat 是否保留原始图片格式
	// true: PNG 保持 PNG 格式（无损），JPEG 保持 JPEG 格式
	// false: 统一转为 JPEG 格式（压缩效果更好，但有损）
	KeepFormat bool
}

var (
	// DefaultCompressOptions 默认压缩选项
	DefaultCompressOptions = CompressOptions{
		MaxSize:    DefaultImageMaxSize,
		Quality:    DefaultJPEGQuality,
		KeepFormat: true, // 默认保留格式，避免颜色丢失
	}

	// globalMediaRootDir 全局媒体存储根目录
	globalMediaRootDir = ""
)

// SetGlobalMediaRootDir 设置全局媒体存储根目录
func SetGlobalMediaRootDir(dir string) {
	// 在设置时直接转化为绝对路径，确保后续拼接出来的全是绝对路径
	if absDir, err := filepath.Abs(dir); err == nil {
		globalMediaRootDir = absDir
	} else {
		globalMediaRootDir = dir
	}
}

// IsBase64Image 检查是否为 base64 格式图片
func IsBase64Image(s string) bool {
	return strings.HasPrefix(s, "data:image/")
}

// IsExternalURL 检查是否为外部 URL
func IsExternalURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// LoadImageFromURL 从外部 URL 下载图片并转为 base64 格式（带压缩）
func LoadImageFromURL(url string) (string, error) {
	return LoadImageFromURLWithMaxSize(url, DefaultImageMaxSize)
}

// LoadImageFromURLWithMaxSize 从外部 URL 下载图片并转为 base64 格式（带压缩，指定最大尺寸）
func LoadImageFromURLWithMaxSize(url string, maxSize int) (string, error) {
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to download image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download image: status code %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read image data: %w", err)
	}

	mimeType := resp.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}

	originalFormat := "png"
	switch {
	case strings.Contains(mimeType, "jpeg"), strings.Contains(mimeType, "jpg"):
		originalFormat = "jpeg"
	case strings.Contains(mimeType, "gif"):
		originalFormat = "gif"
	case strings.Contains(mimeType, "webp"):
		originalFormat = "webp"
	case strings.Contains(mimeType, "bmp"):
		originalFormat = "bmp"
	}

	compressedData, compressedMimeType, err := CompressImage(data, originalFormat, maxSize)
	if err != nil {
		// 压缩失败，使用原始数据
		compressedData = data
		compressedMimeType = mimeType
		if compressedMimeType == "" {
			compressedMimeType = "image/" + originalFormat
		}
	}

	base64Data := base64.StdEncoding.EncodeToString(compressedData)
	return fmt.Sprintf("data:%s;base64,%s", compressedMimeType, base64Data), nil
}

// IsLocalFilePath 检查是否为本地文件路径
func IsLocalFilePath(s string) bool {
	// 排除 URL 和 base64 格式
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "data:") {
		return false
	}
	// file:// 协议也是本地文件，去除前缀后检查
	path := s
	if strings.HasPrefix(s, "file://") {
		path = strings.TrimPrefix(s, "file://")
	}
	// 检查是否为文件路径（以常见图片扩展名结尾）
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".gif" || ext == ".webp" || ext == ".bmp"
}

// ParseBase64Image 解析 base64 图片，返回 mimeType 和 base64Data
func ParseBase64Image(s string) (mimeType, base64Data string) {
	// 格式: data:image/png;base64,iVBORw0KGgo...
	if !strings.HasPrefix(s, "data:image/") {
		return "", ""
	}
	// 查找 base64 数据的起始位置
	commaIdx := strings.Index(s, ",")
	if commaIdx == -1 {
		return "", ""
	}
	// 提取 MIME 类型: data:image/png;base64 -> image/png
	header := s[:commaIdx]
	semiIdx := strings.Index(header, ";")
	if semiIdx == -1 {
		return "", ""
	}
	mimeType = header[5:semiIdx] // 去掉 "data:" 前缀
	base64Data = s[commaIdx+1:]
	return mimeType, base64Data
}

// LoadLocalImage 加载本地图片并转为 base64 格式（带压缩）
func LoadLocalImage(path string) (string, error) {
	return LoadLocalImageWithMaxSize(path, DefaultImageMaxSize)
}

// LoadLocalImageWithMaxSize 加载本地图片并转为 base64 格式（带压缩，指定最大尺寸）
func LoadLocalImageWithMaxSize(path string, maxSize int) (string, error) {
	// 处理 file:// 协议前缀
	if strings.HasPrefix(path, "file://") {
		path = strings.TrimPrefix(path, "file://")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read image file: %w", err)
	}

	ext := strings.ToLower(filepath.Ext(path))
	originalFormat := "png"
	switch ext {
	case ".jpg", ".jpeg":
		originalFormat = "jpeg"
	case ".gif":
		originalFormat = "gif"
	case ".webp":
		originalFormat = "webp"
	case ".bmp":
		originalFormat = "bmp"
	}

	compressedData, mimeType, err := CompressImage(data, originalFormat, maxSize)
	if err != nil {
		// 压缩失败，使用原始数据
		mimeType = "image/" + originalFormat
		if originalFormat == "jpeg" {
			mimeType = "image/jpeg"
		}
		compressedData = data
	}

	base64Data := base64.StdEncoding.EncodeToString(compressedData)
	return fmt.Sprintf("data:%s;base64,%s", mimeType, base64Data), nil
}

// CompressImage 压缩图片，返回压缩后的数据和 MIME 类型
// 使用默认压缩选项（保留格式，质量 92）
func CompressImage(data []byte, originalFormat string, maxSize int) ([]byte, string, error) {
	opts := DefaultCompressOptions
	opts.MaxSize = maxSize
	return CompressImageWithOptions(data, originalFormat, opts)
}

// CompressImageWithOptions 使用指定选项压缩图片
func CompressImageWithOptions(data []byte, originalFormat string, opts CompressOptions) ([]byte, string, error) {
	// 设置默认值
	if opts.MaxSize <= 0 {
		opts.MaxSize = DefaultImageMaxSize
	}
	if opts.Quality <= 0 || opts.Quality > 100 {
		opts.Quality = DefaultJPEGQuality
	}

	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("failed to decode image: %w", err)
	}

	bounds := img.Bounds()
	origWidth := bounds.Dx()
	origHeight := bounds.Dy()

	needsResize := origWidth > opts.MaxSize || origHeight > opts.MaxSize

	var resultImg image.Image = img
	var newWidth, newHeight int

	if needsResize {
		// 计算缩放比例
		if origWidth > origHeight {
			newWidth = opts.MaxSize
			newHeight = origHeight * opts.MaxSize / origWidth
		} else {
			newHeight = opts.MaxSize
			newWidth = origWidth * opts.MaxSize / origHeight
		}

		// 创建缩放后的图片
		resized := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))
		draw.CatmullRom.Scale(resized, resized.Bounds(), img, bounds, draw.Over, nil)
		resultImg = resized
	}

	var buf bytes.Buffer
	var mimeType string

	if opts.KeepFormat {
		switch format {
		case "png":
			// PNG 无损编码
			if err := png.Encode(&buf, resultImg); err != nil {
				return nil, "", fmt.Errorf("failed to encode PNG: %w", err)
			}
			mimeType = "image/png"
		case "jpeg", "jpg":
			if err := jpeg.Encode(&buf, resultImg, &jpeg.Options{Quality: opts.Quality}); err != nil {
				return nil, "", fmt.Errorf("failed to encode JPEG: %w", err)
			}
			mimeType = "image/jpeg"
		default:
			// 其他格式统一转 PNG（无损）
			if err := png.Encode(&buf, resultImg); err != nil {
				return nil, "", fmt.Errorf("failed to encode image: %w", err)
			}
			mimeType = "image/png"
		}
	} else {
		// 统一转 JPEG
		if err := jpeg.Encode(&buf, resultImg, &jpeg.Options{Quality: opts.Quality}); err != nil {
			return nil, "", fmt.Errorf("failed to encode JPEG: %w", err)
		}
		mimeType = "image/jpeg"
	}

	return buf.Bytes(), mimeType, nil
}

// LoadLocalImageWithOptions 加载本地图片并转为 base64 格式（使用指定压缩选项）
func LoadLocalImageWithOptions(path string, opts CompressOptions) (string, error) {
	// 处理 file:// 协议前缀
	if strings.HasPrefix(path, "file://") {
		path = strings.TrimPrefix(path, "file://")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read image file: %w", err)
	}

	ext := strings.ToLower(filepath.Ext(path))
	originalFormat := "png"
	switch ext {
	case ".jpg", ".jpeg":
		originalFormat = "jpeg"
	case ".gif":
		originalFormat = "gif"
	case ".webp":
		originalFormat = "webp"
	case ".bmp":
		originalFormat = "bmp"
	}

	compressedData, mimeType, err := CompressImageWithOptions(data, originalFormat, opts)
	if err != nil {
		// 压缩失败，使用原始数据
		mimeType = "image/" + originalFormat
		if originalFormat == "jpeg" {
			mimeType = "image/jpeg"
		}
		compressedData = data
	}

	base64Data := base64.StdEncoding.EncodeToString(compressedData)
	return fmt.Sprintf("data:%s;base64,%s", mimeType, base64Data), nil
}

// SaveBase64ToTempFile 将 base64 图片保存到临时文件，返回文件路径
// 用于非视觉模型场景：将 base64 图片保存到临时文件，使模型可以通过文件路径将图片传递给图像分析工具
func SaveBase64ToTempFile(base64Str string) (string, error) {
	return SaveBase64WithContext(base64Str, "")
}

// SaveBase64WithContext 将 base64 图片保存到指定上下文目录（根目录/agentID/日期/文件名），返回文件路径
func SaveBase64WithContext(base64Str string, agentID string) (string, error) {
	mimeType, base64Data := ParseBase64Image(base64Str)
	if mimeType == "" || base64Data == "" {
		return "", fmt.Errorf("invalid base64 image format")
	}

	data, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return "", fmt.Errorf("failed to decode base64 data: %w", err)
	}

	ext := ".png"
	switch mimeType {
	case "image/jpeg", "image/jpg":
		ext = ".jpg"
	case "image/gif":
		ext = ".gif"
	case "image/webp":
		ext = ".webp"
	case "image/bmp":
		ext = ".bmp"
	}

	var rootDir string
	if globalMediaRootDir != "" {
		rootDir = globalMediaRootDir
	} else {
		// 回退到临时目录下的 images
		rootDir = filepath.Join(os.TempDir(), "images")
	}

	var dir string
	today := time.Now().Format("2006-01-02")
	if agentID != "" {
		dir = filepath.Join(rootDir, agentID, today)
	} else {
		dir = filepath.Join(rootDir, "default", today)
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create media directory: %w", err)
	}

	filename := fmt.Sprintf("image_%d%s", time.Now().UnixNano(), ext)
	filePath := filepath.Join(dir, filename)

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return "", fmt.Errorf("failed to write image data: %w", err)
	}

	// 因为我们在 SetGlobalMediaRootDir 中已经保证了 globalMediaRootDir 是绝对路径
	// 或者兜底使用 os.TempDir() 也是绝对路径，所以此处 filePath 拼接出来就已经是绝对路径了，直接返回即可
	return filePath, nil
}

// LoadImageFromURLWithOptions 从外部 URL 下载图片并转为 base64 格式（使用指定压缩选项）
func LoadImageFromURLWithOptions(url string, opts CompressOptions) (string, error) {
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to download image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download image: status code %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read image data: %w", err)
	}

	mimeType := resp.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}

	originalFormat := "png"
	switch {
	case strings.Contains(mimeType, "jpeg"), strings.Contains(mimeType, "jpg"):
		originalFormat = "jpeg"
	case strings.Contains(mimeType, "gif"):
		originalFormat = "gif"
	case strings.Contains(mimeType, "webp"):
		originalFormat = "webp"
	case strings.Contains(mimeType, "bmp"):
		originalFormat = "bmp"
	}

	compressedData, compressedMimeType, err := CompressImageWithOptions(data, originalFormat, opts)
	if err != nil {
		// 压缩失败，使用原始数据
		compressedData = data
		compressedMimeType = mimeType
		if compressedMimeType == "" {
			compressedMimeType = "image/" + originalFormat
		}
	}

	base64Data := base64.StdEncoding.EncodeToString(compressedData)
	return fmt.Sprintf("data:%s;base64,%s", compressedMimeType, base64Data), nil
}
