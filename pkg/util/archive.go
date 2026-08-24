package util

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/yeka/zip"
)

// Zip compresses files or directories into a zip file
// source: path to file or directory
// target: path to output zip file
func Zip(source, target string) error {
	return ZipWithPassword(source, target, "")
}

// ZipWithPassword compresses files or directories into a zip file with password
func ZipWithPassword(source, target, password string) error {
	zipFile, err := os.Create(target)
	if err != nil {
		return err
	}
	defer zipFile.Close()

	archive := zip.NewWriter(zipFile)
	defer archive.Close()

	if _, err := os.Stat(source); err != nil {
		return err
	}

	return filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Get relative path from source
		// 获取相对于 source 的路径
		relPath, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}

		// Skip root directory itself
		// 跳过根目录本身
		if relPath == "." {
			return nil
		}

		// Use / as separator in zip file (Standard requirements)
		// ZIP 文件内必须使用 / 作为路径分隔符
		relPath = filepath.ToSlash(relPath)

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}

		// Use relative path as filename in zip
		// 使用相对路径作为压缩包内的文件名
		header.Name = relPath
		if info.IsDir() {
			header.Name += "/"
		} else {
			header.Method = zip.Deflate
		}

		var writer io.Writer
		if password != "" && !info.IsDir() {
			// Set password and encryption method
			// 设置密码和加密方法
			header.SetPassword(password)
			// StandardEncryption is more compatible with Windows built-in ZIP tool
			// StandardEncryption 对 Windows 自带 ZIP 工具兼容性更好
			header.SetEncryptionMethod(zip.StandardEncryption)
			writer, err = archive.CreateHeader(header)
		} else {
			writer, err = archive.CreateHeader(header)
		}

		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		_, err = io.Copy(writer, file)
		return err
	})
}

// UnzipWithPassword extracts a zip archive into the target directory,
// optionally decrypting password-protected entries (ZipCrypto and AES).
// Paths inside the archive are cleaned to prevent Zip Slip attacks.
// UnzipWithPassword 将 zip 压缩包解压到目标目录，支持加密条目（ZipCrypto 与 AES）。
// 压缩包内的路径会被清理以防止 Zip Slip 攻击。
func UnzipWithPassword(source, targetDir, password string) error {
	reader, err := zip.OpenReader(source)
	if err != nil {
		return err
	}
	defer reader.Close()

	for _, f := range reader.File {
		// Clean the archive path and confine it within targetDir
		// 清理压缩包内路径并限制在 targetDir 内
		name := filepath.Clean(f.Name)
		if strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			return fmt.Errorf("illegal file path in zip: %s", f.Name)
		}
		destPath := filepath.Join(targetDir, name)

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(destPath, 0o755); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return err
		}

		if f.IsEncrypted() {
			f.SetPassword(password)
		}

		src, err := f.Open()
		if err != nil {
			return err
		}

		dest, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
		if err != nil {
			src.Close()
			return err
		}

		_, err = io.Copy(dest, src)
		src.Close()
		closeErr := dest.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

// ZipBytes creates a zip archive from a map of filenames and their contents (bytes)
func ZipBytes(files map[string][]byte, target string) error {
	zipFile, err := os.Create(target)
	if err != nil {
		return err
	}
	defer zipFile.Close()

	archive := zip.NewWriter(zipFile)
	defer archive.Close()

	for name, content := range files {
		writer, err := archive.Create(name)
		if err != nil {
			return err
		}
		_, err = writer.Write(content)
		if err != nil {
			return err
		}
	}

	return nil
}
