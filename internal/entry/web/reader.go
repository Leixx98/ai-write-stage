package web

import (
	"bytes"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

var generatedUnitImagePattern = regexp.MustCompile(`^\.\./drafts/(\d+)\.units/(\d+)\.(?:png|jpg|jpeg|webp)$`)

type readerChapter struct {
	Chapter int    `json:"chapter"`
	Title   string `json:"title"`
}

type readerChapterDocument struct {
	Chapter int    `json:"chapter"`
	Title   string `json:"title"`
	HTML    string `json:"html"`
}

func (c *v2Controller) readerChapters(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		envelopeErr(w, http.StatusMethodNotAllowed, codeInvalidRequest, fmt.Errorf("method not allowed"))
		return
	}
	chapters, novelName, err := c.loadReaderChapters()
	if err != nil {
		envelopeErr(w, http.StatusInternalServerError, codeConflict, err)
		return
	}
	envelope(w, http.StatusOK, 0, map[string]any{"novel_name": novelName, "chapters": chapters}, "")
}

func (c *v2Controller) readerChapter(w http.ResponseWriter, r *http.Request, rawChapter string) {
	if r.Method != http.MethodGet {
		envelopeErr(w, http.StatusMethodNotAllowed, codeInvalidRequest, fmt.Errorf("method not allowed"))
		return
	}
	chapter, err := strconv.Atoi(strings.TrimSpace(rawChapter))
	if err != nil || chapter <= 0 || chapter > 100000 {
		envelopeErr(w, http.StatusBadRequest, codeInvalidRequest, fmt.Errorf("invalid chapter"))
		return
	}
	progress, err := c.st.Progress.Load()
	if err != nil {
		envelopeErr(w, http.StatusInternalServerError, codeConflict, err)
		return
	}
	if progress == nil || !slices.Contains(progress.CompletedChapters, chapter) {
		envelopeErr(w, http.StatusNotFound, codeNotFound, fmt.Errorf("formal chapter not found"))
		return
	}
	content, err := c.st.Drafts.LoadChapterText(chapter)
	if err != nil {
		envelopeErr(w, http.StatusInternalServerError, codeConflict, err)
		return
	}
	if strings.TrimSpace(content) == "" {
		envelopeErr(w, http.StatusNotFound, codeNotFound, fmt.Errorf("formal chapter is empty"))
		return
	}
	title := c.readerChapterTitle(chapter)
	html, err := renderReaderMarkdown(chapter, content)
	if err != nil {
		envelopeErr(w, http.StatusInternalServerError, codeConflict, err)
		return
	}
	envelope(w, http.StatusOK, 0, readerChapterDocument{Chapter: chapter, Title: title, HTML: html}, "")
}

func (c *v2Controller) loadReaderChapters() ([]readerChapter, string, error) {
	progress, err := c.st.Progress.Load()
	if err != nil {
		return nil, "", err
	}
	if progress == nil {
		return []readerChapter{}, "", nil
	}
	numbers := append([]int(nil), progress.CompletedChapters...)
	sort.Ints(numbers)
	chapters := make([]readerChapter, 0, len(numbers))
	for _, chapter := range numbers {
		content, err := c.st.Drafts.LoadChapterText(chapter)
		if err != nil {
			return nil, "", err
		}
		if strings.TrimSpace(content) == "" {
			continue
		}
		chapters = append(chapters, readerChapter{Chapter: chapter, Title: c.readerChapterTitle(chapter)})
	}
	return chapters, progress.NovelName, nil
}

func (c *v2Controller) readerChapterTitle(chapter int) string {
	if plan, err := c.st.Drafts.LoadChapterPlan(chapter); err == nil && plan != nil && strings.TrimSpace(plan.Title) != "" {
		return strings.TrimSpace(plan.Title)
	}
	if outline, err := c.st.Outline.LoadOutline(); err == nil {
		for _, item := range outline {
			if item.Chapter == chapter && strings.TrimSpace(item.Title) != "" {
				return strings.TrimSpace(item.Title)
			}
		}
	}
	return fmt.Sprintf("第 %d 章", chapter)
}

func renderReaderMarkdown(chapter int, source string) (string, error) {
	markdown := goldmark.New(goldmark.WithExtensions(extension.GFM))
	data := []byte(source)
	document := markdown.Parser().Parse(text.NewReader(data))
	err := ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		image, ok := node.(*ast.Image)
		if !ok {
			return ast.WalkContinue, nil
		}
		clean := path.Clean(strings.ReplaceAll(string(image.Destination), `\`, "/"))
		matches := generatedUnitImagePattern.FindStringSubmatch(clean)
		if len(matches) != 3 {
			return ast.WalkContinue, nil
		}
		imageChapter, chapterErr := strconv.Atoi(matches[1])
		ordinal, ordinalErr := strconv.Atoi(matches[2])
		if chapterErr != nil || ordinalErr != nil || imageChapter != chapter || ordinal <= 0 {
			image.Destination = []byte("/api/units/not-found")
			return ast.WalkContinue, nil
		}
		image.Destination = []byte(fmt.Sprintf("/api/units/%d/%d", chapter, ordinal))
		return ast.WalkContinue, nil
	})
	if err != nil {
		return "", err
	}
	var rendered bytes.Buffer
	if err := markdown.Renderer().Render(&rendered, data, document); err != nil {
		return "", err
	}
	return rendered.String(), nil
}
