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

// DefaultImageMaxSize is the maximum size of the default image
const DefaultImageMaxSize = 1024

// DefaultJPEGQuality Defaults to JPEG compression quality
const DefaultJPEGQuality = 92

// CompressOptions image compression option
type CompressOptions struct {
	// MaxSize: The maximum image size (width or height); if exceeded, scale proportionally
	MaxSize int
	// Quality JPEG compressed quality (1-100); the higher the value, the better the image quality but the larger the file
	// Recommendation: Use 95+ for color-sensitive scenes, 85-92 for normal scenes
	Quality int
	// KeepFormat retains the original image format
	// true: PNG remains in PNG format (lossless), JPEG remains in JPEG format
	// false: Uniformly converted to JPEG format (better compression but lossy)
	KeepFormat bool
}

var (
	// DefaultCompressOptions is the default compression option
	DefaultCompressOptions = CompressOptions{
		MaxSize:    DefaultImageMaxSize,
		Quality:    DefaultJPEGQuality,
		KeepFormat: true, // Default format retention prevents color loss
	}

	// globalMediaRootDir Global Media Storage Root
	globalMediaRootDir = ""
)

// SetGlobalMediaRootDir sets the global media storage root
func SetGlobalMediaRootDir(dir string) {
	// During setup, it is directly converted to absolute paths to ensure that all subsequent stitching is absolute paths
	if absDir, err := filepath.Abs(dir); err == nil {
		globalMediaRootDir = absDir
	} else {
		globalMediaRootDir = dir
	}
}

// IsBase64Image checks whether the image is in base64 format
func IsBase64Image(s string) bool {
	return strings.HasPrefix(s, "data:image/")
}

// IsExternalURL checks whether it is an external URL
func IsExternalURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// LoadImageFromURL Download images from external URLs and convert them to base64 format (with compression)
func LoadImageFromURL(url string) (string, error) {
	return LoadImageFromURLWithMaxSize(url, DefaultImageMaxSize)
}

// LoadImageFromURLWithMaxSize Download images from external URLs and convert them to base64 format (with compression, specify maximum size)
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
		// Compression failed, using raw data
		compressedData = data
		compressedMimeType = mimeType
		if compressedMimeType == "" {
			compressedMimeType = "image/" + originalFormat
		}
	}

	base64Data := base64.StdEncoding.EncodeToString(compressedData)
	return fmt.Sprintf("data:%s;base64,%s", compressedMimeType, base64Data), nil
}

// IsLocalFilePath checks whether it is a local file path
func IsLocalFilePath(s string) bool {
	// Excluding URLs and base64 formats
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "data:") {
		return false
	}
	// file:// protocol is also a local file, which is checked after removing the prefix
	path := s
	if strings.HasPrefix(s, "file://") {
		path = strings.TrimPrefix(s, "file://")
	}
	// Check if it is a file path (ending with a common image extension)
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".gif" || ext == ".webp" || ext == ".bmp"
}

// ParseBase64Image parses the base64 image, returning mimeType and base64Data
func ParseBase64Image(s string) (mimeType, base64Data string) {
	// Format: data:image/png; base64,iVBORw0KGgo...
	if !strings.HasPrefix(s, "data:image/") {
		return "", ""
	}
	// Find the starting position of the base64 data
	commaIdx := strings.Index(s, ",")
	if commaIdx == -1 {
		return "", ""
	}
	// Extract MIME type: data:image/png; base64 -> image/png
	header := s[:commaIdx]
	semiIdx := strings.Index(header, ";")
	if semiIdx == -1 {
		return "", ""
	}
	mimeType = header[5:semiIdx] // Remove the "data:" prefix
	base64Data = s[commaIdx+1:]
	return mimeType, base64Data
}

// LoadLocalImage loads the local image and converts it to base64 format (with compression)
func LoadLocalImage(path string) (string, error) {
	return LoadLocalImageWithMaxSize(path, DefaultImageMaxSize)
}

// LoadLocalImageWithMaxSize loads the local image and converts it to base64 format (with compression, specifying maximum size)
func LoadLocalImageWithMaxSize(path string, maxSize int) (string, error) {
	// Handle file:// protocol prefixes
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
		// Compression failed, using raw data
		mimeType = "image/" + originalFormat
		if originalFormat == "jpeg" {
			mimeType = "image/jpeg"
		}
		compressedData = data
	}

	base64Data := base64.StdEncoding.EncodeToString(compressedData)
	return fmt.Sprintf("data:%s;base64,%s", mimeType, base64Data), nil
}

// CompressImage compresses images and returns the compressed data and MIME type
// Use the default compression option (keep format, quality 92)
func CompressImage(data []byte, originalFormat string, maxSize int) ([]byte, string, error) {
	opts := DefaultCompressOptions
	opts.MaxSize = maxSize
	return CompressImageWithOptions(data, originalFormat, opts)
}

// CompressImageWithOptions compresses images using the specified option
func CompressImageWithOptions(data []byte, originalFormat string, opts CompressOptions) ([]byte, string, error) {
	// Set the default value
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
		// Calculate the scaling ratio
		if origWidth > origHeight {
			newWidth = opts.MaxSize
			newHeight = origHeight * opts.MaxSize / origWidth
		} else {
			newHeight = opts.MaxSize
			newWidth = origWidth * opts.MaxSize / origHeight
		}

		// Create a scaled image
		resized := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))
		draw.CatmullRom.Scale(resized, resized.Bounds(), img, bounds, draw.Over, nil)
		resultImg = resized
	}

	var buf bytes.Buffer
	var mimeType string

	if opts.KeepFormat {
		switch format {
		case "png":
			// PNG lossless encoding
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
			// Other formats are uniformly converted to PNG (lossless)
			if err := png.Encode(&buf, resultImg); err != nil {
				return nil, "", fmt.Errorf("failed to encode image: %w", err)
			}
			mimeType = "image/png"
		}
	} else {
		// Unified conversion to JPEG
		if err := jpeg.Encode(&buf, resultImg, &jpeg.Options{Quality: opts.Quality}); err != nil {
			return nil, "", fmt.Errorf("failed to encode JPEG: %w", err)
		}
		mimeType = "image/jpeg"
	}

	return buf.Bytes(), mimeType, nil
}

// LoadLocalImageWithOptions Load the local image and convert it to base64 format (using the specified compression option)
func LoadLocalImageWithOptions(path string, opts CompressOptions) (string, error) {
	// Handle file:// protocol prefixes
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
		// Compression failed, using raw data
		mimeType = "image/" + originalFormat
		if originalFormat == "jpeg" {
			mimeType = "image/jpeg"
		}
		compressedData = data
	}

	base64Data := base64.StdEncoding.EncodeToString(compressedData)
	return fmt.Sprintf("data:%s;base64,%s", mimeType, base64Data), nil
}

// SaveBase64ToTempFile saves the base64 image to a temporary file and returns to the file path
// For non-visual model scenarios: Save base64 images to temporary files, allowing the model to pass images to image analysis tools via file paths
func SaveBase64ToTempFile(base64Str string) (string, error) {
	return SaveBase64WithContext(base64Str, "")
}

// SaveBase64WithContext saves the base64 image to the specified context directory (root/agentID/date/filename), returning the file path
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
		// Fall back to images in the temporary directory
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

	// Because we have already ensured in SetGlobalMediaRootDir that globalMediaRootDir is an absolute path
	// Alternatively, use os.TempDir () as a backup method, which is also an absolute path, so concatenating filePath here already counts as an absolute path, so just return directly
	return filePath, nil
}

// LoadImageFromURLWithOptions Download images from external URLs and convert them to base64 format (using specified compression options)
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
		// Compression failed, using raw data
		compressedData = data
		compressedMimeType = mimeType
		if compressedMimeType == "" {
			compressedMimeType = "image/" + originalFormat
		}
	}

	base64Data := base64.StdEncoding.EncodeToString(compressedData)
	return fmt.Sprintf("data:%s;base64,%s", compressedMimeType, base64Data), nil
}
