package store

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/voocel/ainovel-cli/internal/domain"
)

// DraftStore 管理章节构思、草稿和终稿。
type DraftStore struct{ io *IO }

func NewDraftStore(io *IO) *DraftStore { return &DraftStore{io: io} }

// SaveChapterPlan 保存章节构思到 drafts/{ch}.plan.json。
func (s *DraftStore) SaveChapterPlan(plan domain.ChapterPlan) error {
	return s.io.WriteJSON(fmt.Sprintf("drafts/%02d.plan.json", plan.Chapter), plan)
}

// LoadChapterPlan 读取章节构思。
func (s *DraftStore) LoadChapterPlan(chapter int) (*domain.ChapterPlan, error) {
	var plan domain.ChapterPlan
	if err := s.io.ReadJSON(fmt.Sprintf("drafts/%02d.plan.json", chapter), &plan); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &plan, nil
}

func writingUnitPath(chapter, ordinal int) string {
	return fmt.Sprintf("drafts/%02d.units/%03d.md", chapter, ordinal)
}

// LoadWritingProgress 从章节计划和独立 unit 工件推导进度。连续前缀之外的孤立
// unit 不计为已完成，避免损坏工件使 Writer 跳过中间正文。
func (s *DraftStore) LoadWritingProgress(chapter int) (*domain.WritingProgress, error) {
	plan, err := s.LoadChapterPlan(chapter)
	if err != nil || plan == nil {
		return nil, err
	}
	assignments := plan.WritingUnits()
	progress := &domain.WritingProgress{Chapter: chapter, TotalUnits: len(assignments)}
	if len(assignments) == 0 {
		return progress, nil
	}

	var parts []string
	for i, assignment := range assignments {
		data, readErr := s.io.ReadFile(writingUnitPath(chapter, i+1))
		if os.IsNotExist(readErr) {
			next := assignment
			progress.Next = &next
			break
		}
		if readErr != nil {
			return nil, readErr
		}
		parts = append(parts, string(data))
		progress.CompletedUnits++
		progress.CompletedUnitIDs = append(progress.CompletedUnitIDs, assignment.Unit.ID)
	}
	if progress.CompletedUnits == len(assignments) {
		progress.Complete = true
	}
	if len(parts) > 0 {
		progress.PreviousTail = tailRunes(strings.Join(parts, "\n\n"), 600)
	}
	return progress, nil
}

// SaveWritingUnit 幂等保存当前 unit，并按已完成的连续前缀重建整章草稿。
// unit 文件是事实源，draft.md 是每次可重建的投影，避免 append 崩溃重试重复正文。
func (s *DraftStore) SaveWritingUnit(chapter, ordinal, totalUnits, rebuildThrough int, content string) (bool, string, error) {
	if chapter <= 0 || ordinal <= 0 || totalUnits <= 0 || ordinal > totalUnits ||
		rebuildThrough < ordinal || rebuildThrough > totalUnits || strings.TrimSpace(content) == "" {
		return false, "", fmt.Errorf("invalid writing unit chapter=%d ordinal=%d total=%d rebuild_through=%d", chapter, ordinal, totalUnits, rebuildThrough)
	}
	path := writingUnitPath(chapter, ordinal)
	alreadyExists := false
	var assembled string
	err := s.io.WithWriteLock(func() error {
		existing, err := s.io.ReadFileUnlocked(path)
		switch {
		case err == nil:
			alreadyExists = true
			content = string(existing)
		case !os.IsNotExist(err):
			return err
		default:
			err = nil
		}

		var parts []string
		for i := 1; i <= rebuildThrough; i++ {
			var data []byte
			if i == ordinal && !alreadyExists {
				data = []byte(content)
				err = nil
			} else {
				data, err = s.io.ReadFileUnlocked(writingUnitPath(chapter, i))
			}
			if os.IsNotExist(err) {
				return fmt.Errorf("writing unit prefix missing at ordinal %d", i)
			}
			if err != nil {
				return err
			}
			parts = append(parts, strings.TrimSpace(string(data)))
		}
		assembled = strings.Join(slices.DeleteFunc(parts, func(part string) bool { return part == "" }), "\n\n")
		// draft 是 unit 工件的可重建投影。先写投影再提交新 unit，失败时门禁会因
		// unit 尚未完成而阻止检查/提交；反向顺序会留下“unit 完成但 draft 落后”。
		if err := s.io.WriteFileUnlocked(fmt.Sprintf("drafts/%02d.draft.md", chapter), []byte(assembled)); err != nil {
			return err
		}
		if !alreadyExists {
			return s.io.WriteFileUnlocked(path, []byte(content))
		}
		return nil
	})
	return alreadyExists, assembled, err
}

func tailRunes(text string, max int) string {
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return string(runes[len(runes)-max:])
}

// SaveDraft 保存整章草稿到 drafts/{ch}.draft.md。
func (s *DraftStore) SaveDraft(chapter int, content string) error {
	return s.io.WriteMarkdown(fmt.Sprintf("drafts/%02d.draft.md", chapter), content)
}

// AppendDraft 追加内容到现有草稿（续写模式）。
func (s *DraftStore) AppendDraft(chapter int, content string) error {
	rel := fmt.Sprintf("drafts/%02d.draft.md", chapter)
	return s.io.WithWriteLock(func() error {
		existing, err := s.io.ReadFileUnlocked(rel)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		var merged string
		if len(existing) > 0 {
			merged = string(existing) + "\n\n" + content
		} else {
			merged = content
		}
		return s.io.WriteFileUnlocked(rel, []byte(merged))
	})
}

// LoadDraft 读取整章草稿。
func (s *DraftStore) LoadDraft(chapter int) (string, error) {
	data, err := s.io.ReadFile(fmt.Sprintf("drafts/%02d.draft.md", chapter))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// LoadChapterContent 加载章节草稿正文及字数。
func (s *DraftStore) LoadChapterContent(chapter int) (string, int, error) {
	draft, err := s.LoadDraft(chapter)
	if err != nil {
		return "", 0, err
	}
	if draft != "" {
		return draft, utf8.RuneCountInString(draft), nil
	}
	return "", 0, nil
}

// SaveFinalChapter 保存最终章节正文到 chapters/{ch}.md。
func (s *DraftStore) SaveFinalChapter(chapter int, content string) error {
	return s.io.WriteMarkdown(fmt.Sprintf("chapters/%02d.md", chapter), content)
}

// LoadChapterText 读取已提交的终稿原文。
func (s *DraftStore) LoadChapterText(chapter int) (string, error) {
	data, err := s.io.ReadFile(fmt.Sprintf("chapters/%02d.md", chapter))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// LoadChapterRange 读取指定范围的终稿原文片段。
func (s *DraftStore) LoadChapterRange(from, to, maxRunes int) (map[int]string, error) {
	result := make(map[int]string)
	for ch := from; ch <= to; ch++ {
		text, err := s.LoadChapterText(ch)
		if err != nil {
			return nil, err
		}
		if text == "" {
			continue
		}
		if maxRunes > 0 {
			runes := []rune(text)
			if len(runes) > maxRunes {
				text = string(runes[:maxRunes]) + "..."
			}
		}
		result[ch] = text
	}
	return result, nil
}

var dialogueRe = regexp.MustCompile(`"[^"]*"`)

// ExtractDialogue 从已提交章节中提取指定角色的对话片段。
// maxCompletedChapter 由调用方传入，避免跨域依赖。
func (s *DraftStore) ExtractDialogue(characterName string, aliases []string, maxSamples, maxCompletedChapter int) ([]string, error) {
	if maxSamples <= 0 {
		maxSamples = 5
	}
	names := append([]string{characterName}, aliases...)

	var samples []string
	var readErrs []error
	for ch := maxCompletedChapter; ch >= 1 && len(samples) < maxSamples; ch-- {
		text, err := s.LoadChapterText(ch)
		if err != nil {
			readErrs = append(readErrs, fmt.Errorf("chapter %d: %w", ch, err))
			continue
		}
		if text == "" {
			continue
		}
		paragraphs := strings.Split(text, "\n")
		for _, para := range paragraphs {
			if len(samples) >= maxSamples {
				break
			}
			found := false
			for _, name := range names {
				if strings.Contains(para, name) {
					found = true
					break
				}
			}
			if !found {
				continue
			}
			matches := dialogueRe.FindAllString(para, -1)
			for _, m := range matches {
				if len(samples) >= maxSamples {
					break
				}
				if utf8.RuneCountInString(m) > 5 {
					samples = append(samples, characterName+": "+m)
				}
			}
		}
	}
	return samples, errors.Join(readErrs...)
}

// ExtractStyleAnchors 从已提交章节中提取代表性段落作为风格锚点。
// maxCompletedChapter 由调用方传入，避免跨域依赖。
func (s *DraftStore) ExtractStyleAnchors(maxAnchors, maxCompletedChapter int) ([]string, error) {
	if maxAnchors <= 0 {
		maxAnchors = 5
	}

	var anchors []string
	var readErrs []error
	for ch := 1; ch <= maxCompletedChapter && len(anchors) < maxAnchors; ch++ {
		text, err := s.LoadChapterText(ch)
		if err != nil {
			readErrs = append(readErrs, fmt.Errorf("chapter %d: %w", ch, err))
			continue
		}
		if text == "" {
			continue
		}
		paragraphs := strings.Split(text, "\n\n")
		for _, para := range paragraphs {
			if len(anchors) >= maxAnchors {
				break
			}
			para = strings.TrimSpace(para)
			runeCount := utf8.RuneCountInString(para)
			if runeCount < 50 || runeCount > 300 {
				continue
			}
			if strings.Count(para, "\u201c") > 2 {
				continue
			}
			anchors = append(anchors, para)
		}
	}
	return anchors, errors.Join(readErrs...)
}
