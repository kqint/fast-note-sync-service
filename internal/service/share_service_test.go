// Package service implements the business logic layer.
// Package service 实现业务逻辑层。
package service

import (
	"testing"

	"github.com/haierkeys/fast-note-sync-service/internal/domain"
	"github.com/stretchr/testify/assert"
)

// TestExtractSharedNoteFileRefs verifies that all image/file refs in markdown content are extracted.
// TestExtractSharedNoteFileRefs 验证 markdown 内容中所有图片/文件引用都被正确提取。
func TestExtractSharedNoteFileRefs(t *testing.T) {
	content := `
![[assets/photo.png|240]]
![inline](../images/demo.jpg "title")
<img src="./img/html.png" alt="demo">
`
	refs := extractSharedNoteFileRefs(content)

	expected := map[string]struct{}{
		"assets/photo.png":   {},
		"../images/demo.jpg": {},
		"./img/html.png":     {},
	}

	assert.Len(t, refs, len(expected), "should extract exactly %d file refs", len(expected))

	for _, ref := range refs {
		_, ok := expected[ref]
		assert.True(t, ok, "unexpected ref extracted: %s", ref)
	}
}

// TestBuildSharePathCandidates verifies that relative image paths are resolved correctly.
// TestBuildSharePathCandidates 验证相对图片路径被正确解析为候选路径。
func TestBuildSharePathCandidates(t *testing.T) {
	candidates := buildSharePathCandidates("notes/daily/today.md", "../images/demo.png")
	expected := []string{"notes/images/demo.png"}

	assert.Equal(t, expected, candidates, "resolved path candidates should match")
}

// TestBuildSharePathCandidatesPercentEncoded ensures percent-encoded markdown
// image targets (vault-absolute paths produced by Obsidian for ![alt](url))
// are decoded so DB lookups against literal Obsidian paths succeed.
// TestBuildSharePathCandidatesPercentEncoded 验证百分号编码的 Markdown 图片
// 路径（Obsidian 在 ![alt](url) 中产生的库内绝对路径）会被解码，从而能匹配
// 数据库里的字面路径。
func TestBuildSharePathCandidatesPercentEncoded(t *testing.T) {
	t.Run("vault-absolute with percent-encoded space", func(t *testing.T) {
		candidates := buildSharePathCandidates(
			"图片显示测试/test note.md",
			"图片显示测试/assets/test/32824b540923dd543957abdcd109b3de9d8248eb%201.jpg",
		)
		expected := []string{
			"图片显示测试/图片显示测试/assets/test/32824b540923dd543957abdcd109b3de9d8248eb 1.jpg",
			"图片显示测试/assets/test/32824b540923dd543957abdcd109b3de9d8248eb 1.jpg",
		}
		assert.Equal(t, expected, candidates)
	})

	t.Run("relative with percent-encoded space", func(t *testing.T) {
		candidates := buildSharePathCandidates(
			"notes/today.md",
			"../images/demo%20pic.png",
		)
		expected := []string{"images/demo pic.png"}
		assert.Equal(t, expected, candidates)
	})
}

// TestRewriteMarkdownImageLinks verifies that markdown image links are rewritten to share URLs.
// TestRewriteMarkdownImageLinks 验证 markdown 图片链接被重写为分享 URL。
func TestRewriteMarkdownImageLinks(t *testing.T) {
	content := `![demo](./images/demo.png "title")`
	fileRefs := map[string]*domain.File{
		"./images/demo.png": {ID: 42},
	}

	rewritten := rewriteMarkdownImageLinks(content, fileRefs, "share-token", "pwd")
	expected := `![demo](/api/share/file?id=42&share_token=share-token&password=pwd "title")`

	assert.Equal(t, expected, rewritten, "image links should be rewritten to share API URLs")
}
