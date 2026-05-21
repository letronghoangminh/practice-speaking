package services

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
)

const maxDocumentBytes = 8 << 20
const pdfHeaderScanLimit = 1024

var (
	pdfGluedSentenceRE = regexp.MustCompile(`([[:lower:]0-9%])\.([[:upper:]])`)
	pdfSectionRE       = regexp.MustCompile(`(SUMMARY|WORK EXPERIENCE|WORK EXPERIENCES|EDUCATION|CERTIFICATIONS|HONORS & AWARDS|SKILLS|ADDITIONAL INFORMATION)`)
)

type uploadPolicy struct {
	label      string
	extensions map[string]bool
	images     bool
}

var (
	jdUploadPolicy = uploadPolicy{
		label: "JD file",
		extensions: map[string]bool{
			".txt":  true,
			".md":   true,
			".pdf":  true,
			".png":  true,
			".jpg":  true,
			".jpeg": true,
			".webp": true,
		},
		images: true,
	}
	cvUploadPolicy = uploadPolicy{
		label: "CV file",
		extensions: map[string]bool{
			".md":  true,
			".pdf": true,
		},
	}
)

func ExtractJDUploadText(ctx context.Context, ai AIClient, file *multipart.FileHeader, fallback string) (string, error) {
	return extractUploadText(ctx, ai, file, fallback, jdUploadPolicy)
}

func ExtractCVUploadText(ctx context.Context, file *multipart.FileHeader, fallback string) (string, error) {
	return extractUploadText(ctx, nil, file, fallback, cvUploadPolicy)
}

func extractUploadText(ctx context.Context, ai AIClient, file *multipart.FileHeader, fallback string, policy uploadPolicy) (string, error) {
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

	text, err := extractPolicyText(ctx, ai, file.Filename, file.Header.Get("Content-Type"), data, policy)
	if err != nil {
		return "", err
	}
	return text, nil
}

func ExtractDocumentText(name string, data []byte) (string, error) {
	return extractPolicyText(context.Background(), nil, name, "", data, uploadPolicy{
		label: "document",
		extensions: map[string]bool{
			".txt": true,
			".md":  true,
			".pdf": true,
		},
	})
}

func extractPolicyText(ctx context.Context, ai AIClient, name string, contentType string, data []byte, policy uploadPolicy) (string, error) {
	ext := strings.ToLower(filepath.Ext(name))
	if !policy.extensions[ext] {
		return "", fmt.Errorf("unsupported file type %q for %s; use %s", ext, policy.label, policy.allowedDescription())
	}

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
	case ".png", ".jpg", ".jpeg", ".webp":
		if !policy.images {
			return "", fmt.Errorf("unsupported file type %q for %s; use %s", ext, policy.label, policy.allowedDescription())
		}
		if len(data) == 0 {
			return "", fmt.Errorf("%s did not contain image data", name)
		}
		if ai == nil {
			return "", fmt.Errorf("%s image extraction is not configured", policy.label)
		}
		text, err := ai.ExtractImageText(ctx, name, imageContentType(ext, contentType), data)
		if err != nil {
			return "", err
		}
		text = strings.TrimSpace(text)
		if text == "" {
			return "", fmt.Errorf("%s did not contain readable image text", name)
		}
		return text, nil
	default:
		return "", fmt.Errorf("unsupported file type %q for %s; use %s", ext, policy.label, policy.allowedDescription())
	}
}

func (p uploadPolicy) allowedDescription() string {
	if p.images {
		return ".txt, .md, .pdf, .png, .jpg, .jpeg, or .webp"
	}
	if p.extensions[".txt"] {
		return ".txt, .md, or .pdf"
	}
	return ".md or .pdf"
}

func imageContentType(ext string, contentType string) string {
	contentType = strings.TrimSpace(strings.ToLower(strings.Split(contentType, ";")[0]))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	default:
		if strings.HasPrefix(contentType, "image/") {
			return contentType
		}
		return "application/octet-stream"
	}
}

func extractPDFText(name string, data []byte) (string, error) {
	normalized, err := normalizePDFData(name, data)
	if err != nil {
		return "", err
	}

	tmp, err := os.CreateTemp("", "practice-speaking-*.pdf")
	if err != nil {
		return "", fmt.Errorf("create temporary PDF: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(normalized); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write temporary PDF: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close temporary PDF: %w", err)
	}

	f, reader, err := pdf.Open(tmpName)
	if err != nil {
		return "", fmt.Errorf("open PDF %s: %s", name, friendlyPDFError(name, err))
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
	return normalizeExtractedPDFText(buf.String()), nil
}

func normalizeExtractedPDFText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.ReplaceAll(text, "\u00a0", " ")
	text = strings.ReplaceAll(text, "∼", "~")
	text = repairExtractedTextJoins(text)
	text = pdfGluedSentenceRE.ReplaceAllString(text, "$1.\n$2")
	text = pdfSectionRE.ReplaceAllString(text, "\n$1\n")
	text = strings.ReplaceAll(text, "Main responsibilities:", "\nMain responsibilities:\n")
	text = strings.ReplaceAll(text, "Technologies used:", "\nTechnologies used:\n")
	text = strings.ReplaceAll(text, "•", "\n• ")

	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func repairExtractedTextJoins(text string) string {
	return strings.NewReplacer(
		"clustersand", "clusters and",
		"deploymentsmonthly", "deployments monthly",
		"securityawareness", "security awareness",
		"auditlogging", "audit logging",
		"GitOpsworkflows", "GitOps workflows",
		"projects ,", "projects,",
	).Replace(text)
}

func normalizePDFData(name string, data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("%s is empty and is not a valid PDF", name)
	}

	limit := min(len(data), pdfHeaderScanLimit)
	headerIndex := bytes.Index(data[:limit], []byte("%PDF-"))
	if headerIndex < 0 {
		return nil, fmt.Errorf("%s is named .pdf but does not look like a valid PDF file. Please export or download it as a real PDF, or upload the CV as .md", name)
	}
	if headerIndex == 0 {
		return data, nil
	}
	return data[headerIndex:], nil
}

func friendlyPDFError(name string, err error) string {
	message := strings.TrimSpace(err.Error())
	if strings.Contains(strings.ToLower(message), "invalid header") || strings.Contains(strings.ToLower(message), "not a pdf") {
		return fmt.Sprintf("%s is named .pdf but could not be parsed as a valid PDF. Please export or download it as a real PDF, or upload the CV as .md", name)
	}
	return message
}
