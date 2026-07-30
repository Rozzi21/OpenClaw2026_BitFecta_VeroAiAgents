package handlers

import (
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/dto"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/utils"
)

// maxUploadBytes caps a single media upload (SEC-5).
const maxUploadBytes = 5 << 20 // 5 MiB

func (h *Handler) UploadTripMedia(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		utils.BadRequest(c, "Upload failed: multipart file field 'file' is required", gin.H{})
		return
	}

	// SEC-5: enforce a size limit before touching disk.
	if file.Size <= 0 || file.Size > maxUploadBytes {
		utils.BadRequest(c, "File too large", gin.H{"max_bytes": maxUploadBytes, "size": file.Size})
		return
	}

	ext := strings.ToLower(filepath.Ext(file.Filename))
	allowed := map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".gif": true}
	if !allowed[ext] {
		utils.BadRequest(c, "Unsupported media type", gin.H{"extension": ext})
		return
	}

	// SEC-5: verify the real content type from the file's magic bytes instead of
	// trusting the filename extension alone.
	if detected, err := detectImageContentType(file); err != nil {
		log.Printf("[upload] unable to read file: %v", err)
		utils.BadRequest(c, "Unable to read file", gin.H{})
		return
	} else if !strings.HasPrefix(detected, "image/") {
		utils.BadRequest(c, "File content is not a valid image", gin.H{"detected": detected})
		return
	}

	dir := filepath.Join("uploads", "trips")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		utils.ServerError(c, err)
		return
	}
	filename := uuid.NewString() + ext
	path := filepath.Join(dir, filename)
	if err := c.SaveUploadedFile(file, path); err != nil {
		utils.ServerError(c, err)
		return
	}

	utils.Success(c, http.StatusCreated, "Media uploaded", dto.UploadResponse{
		URL:      "/" + filepath.ToSlash(path),
		Filename: filename,
		Size:     file.Size,
	})
}

// detectImageContentType sniffs the first 512 bytes to determine the true MIME
// type of an uploaded file (SEC-5).
func detectImageContentType(file *multipart.FileHeader) (string, error) {
	f, err := file.Open()
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return "", err
	}
	return http.DetectContentType(buf[:n]), nil
}
