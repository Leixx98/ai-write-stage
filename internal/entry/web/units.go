package web

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	imagesvc "github.com/voocel/ainovel-cli/internal/imagejob/service"
	"github.com/voocel/ainovel-cli/internal/store"
)

func (c *v2Controller) unit(w http.ResponseWriter, r *http.Request, rest string) {
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 1 {
		ch, err := strconv.Atoi(parts[0])
		if err != nil || ch <= 0 {
			envelopeErr(w, 400, codeInvalidRequest, fmt.Errorf("invalid chapter"))
			return
		}
		jobs, _ := c.images.ListJobs()
		var filtered []store.ImageJob
		for _, j := range jobs {
			if imagesvc.IsUnitJob(j) && j.Chapter == ch {
				j.PromptRaw = ""
				filtered = append(filtered, j)
			}
		}
		envelope(w, 200, 0, filtered, "")
		return
	}
	if len(parts) < 3 {
		envelopeErr(w, 404, codeNotFound, fmt.Errorf("unit route not found"))
		return
	}
	ch, e1 := strconv.Atoi(parts[0])
	ord, e2 := strconv.Atoi(parts[1])
	if e1 != nil || e2 != nil {
		envelopeErr(w, 400, codeInvalidRequest, fmt.Errorf("invalid unit"))
		return
	}
	if len(parts) == 4 && parts[2] == "image" && parts[3] == "generate" {
		c.generateUnitImage(w, r, ch, ord)
		return
	}
	if strings.HasSuffix(rest, "/image-job") {
		jobs, _ := c.images.ListJobs()
		for i := len(jobs) - 1; i >= 0; i-- {
			j := jobs[i]
			if imagesvc.IsUnitJob(j) && j.Chapter == ch && j.Ordinal == ord {
				envelope(w, 200, 0, j, "")
				return
			}
		}
		envelopeErr(w, 404, codeNotFound, fmt.Errorf("image job not found"))
		return
	}
	if strings.HasSuffix(rest, "/image/retry") {
		jobs, _ := c.images.ListJobs()
		for _, j := range jobs {
			if imagesvc.IsUnitJob(j) && j.Chapter == ch && j.Ordinal == ord {
				c.job(w, r, j.JobID+"/retry")
				return
			}
		}
		envelopeErr(w, 404, codeNotFound, fmt.Errorf("image job not found"))
		return
	}
	base := filepath.Join(c.rt.Dir(), "drafts", fmt.Sprintf("%02d.units", ch), fmt.Sprintf("%03d", ord))
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".webp"} {
		path := base + ext
		if _, err := os.Stat(path); err == nil {
			w.Header().Set("Content-Type", "image/"+strings.TrimPrefix(ext, "."))
			w.Header().Set("X-API-Code", "0")
			http.ServeFile(w, r, path)
			return
		}
	}
	envelopeErr(w, 404, codeNotFound, fmt.Errorf("unit image not found"))
}

type unitImageGenerateRequest struct {
	ProfileID string `json:"profile_id"`
	Force     bool   `json:"force"`
}

func (c *v2Controller) generateUnitImage(w http.ResponseWriter, r *http.Request, chapter, ordinal int) {
	if r.Method != http.MethodPost {
		envelopeErr(w, 405, codeInvalidRequest, fmt.Errorf("method not allowed"))
		return
	}
	var request unitImageGenerateRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeBody(r, &request); err != nil && !errors.Is(err, io.EOF) {
			envelopeErr(w, 400, codeInvalidRequest, err)
			return
		}
	}
	job, err := c.startUnitImage(chapter, ordinal, request.ProfileID, request.Force)
	if err != nil {
		var conflict imagesvc.ConflictError
		if errors.As(err, &conflict) {
			envelope(w, 409, codeUnitJobConflict, conflict.Job, "同一个 unit 已有图片任务正在运行")
			return
		}
		if errors.Is(err, imagesvc.ErrInvalidUnitIdentity) {
			envelopeErr(w, 400, codeInvalidRequest, err)
			return
		}
		if strings.Contains(err.Error(), "桥接配置无法读取") {
			envelopeErr(w, 500, codeConfigInvalid, err)
			return
		}
		if strings.Contains(err.Error(), "尚未启用") {
			envelopeErr(w, 409, codeConflict, err)
			return
		}
		if strings.Contains(err.Error(), "不存在或为空") || strings.Contains(err.Error(), "计划不存在") || strings.Contains(err.Error(), "outside the chapter plan") {
			envelopeErr(w, 404, codeNotFound, err)
			return
		}
		if errors.Is(err, imagesvc.ErrSave) {
			envelopeErr(w, 500, codeJobFailed, err)
			return
		}
		envelope(w, 422, codePromptSchema, map[string]any{"valid": false}, err.Error())
		return
	}
	envelope(w, http.StatusAccepted, 0, job, "")
}

func (c *v2Controller) startUnitImage(chapter, ordinal int, profileID string, force bool) (store.ImageJob, error) {
	return c.rt.StartUnitImage(chapter, ordinal, profileID, force)
}

func tailText(value string, maximum int) string {
	runes := []rune(value)
	if maximum <= 0 || len(runes) <= maximum {
		return value
	}
	return string(runes[len(runes)-maximum:])
}
