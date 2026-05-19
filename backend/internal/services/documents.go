package services

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
)

const maxDocumentBytes = 8 << 20

func ExtractUploadText(file *multipart.FileHeader, fallback string) (string, error) {
	fallback = strings.TrimSpace(fallback)
	if file == nil {
		return fallback, nil
	}

	opened, err := file.Open()
	if err != nil {
		return "", fmt.Errorf("open upload: %w", err)
	}
	defer opened.Close()

	data, err := io.ReadAll(io.LimitReader(opened, maxDocumentBytes+1))
	if err != nil {
		return "", fmt.Errorf("read upload: %w", err)
	}
	if len(data) > maxDocumentBytes {
		return "", fmt.Errorf("%s is larger than the 8 MB MVP limit", file.Filename)
	}

	text, err := ExtractDocumentText(file.Filename, data)
	if err != nil {
		return "", err
	}
	return text, nil
}

func ExtractDocumentText(name string, data []byte) (string, error) {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".txt", ".md":
		if !utf8.Valid(data) {
			return "", fmt.Errorf("%s must be valid UTF-8 text", name)
		}
		text := strings.TrimSpace(string(data))
		if text == "" {
			return "", fmt.Errorf("%s did not contain text", name)
		}
		return text, nil
	case ".pdf":
		text, err := extractPDFText(name, data)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(text) == "" {
			return "", fmt.Errorf("%s did not contain extractable PDF text", name)
		}
		return strings.TrimSpace(text), nil
	default:
		return "", fmt.Errorf("unsupported file type %q; use .pdf, .txt, or .md", ext)
	}
}

func extractPDFText(name string, data []byte) (string, error) {
	tmp, err := os.CreateTemp("", "practice-speaking-*.pdf")
	if err != nil {
		return "", fmt.Errorf("create temporary PDF: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write temporary PDF: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close temporary PDF: %w", err)
	}

	f, reader, err := pdf.Open(tmpName)
	if err != nil {
		return "", fmt.Errorf("open PDF %s: %w", name, err)
	}
	defer f.Close()

	plain, err := reader.GetPlainText()
	if err != nil {
		return "", fmt.Errorf("extract PDF text from %s: %w", name, err)
	}

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, plain); err != nil {
		return "", fmt.Errorf("read PDF text from %s: %w", name, err)
	}
	return buf.String(), nil
}
