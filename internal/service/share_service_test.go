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
![spaces](en space/img2.jpg)
`
	refs := extractSharedNoteFileRefs(content)

	expected := map[string]struct{}{
		"assets/photo.png":   {},
		"../images/demo.jpg": {},
		"./img/html.png":     {},
		// Strict-parse target (whitespace-split) of the spaces case.
		"en": {},
		// Lenient-parse target of the spaces case.
		"en space/img2.jpg": {},
	}

	assert.Len(t, refs, len(expected), "should extract exactly %d file refs", len(expected))

	for _, ref := range refs {
		_, ok := expected[ref]
		assert.True(t, ok, "unexpected ref extracted: %s", ref)
	}
}

// TestParseMarkdownLinkTargetLenient covers the lenient parser used as a
// fallback for `![alt](body)` whose URL contains literal whitespace.
// TestParseMarkdownLinkTargetLenient 覆盖含字面空白的 ![alt](body) 宽松解析。
func TestParseMarkdownLinkTargetLenient(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantTarget string
		wantTitle  string
	}{
		{"plain", "foo.jpg", "foo.jpg", ""},
		{"literal space no title", "en space/img2.jpg", "en space/img2.jpg", ""},
		{"literal space with title", `en space/img2.jpg "demo"`, "en space/img2.jpg", `"demo"`},
		{"angle bracket", "<en space/img2.jpg>", "en space/img2.jpg", ""},
		{"empty", "", "", ""},
		{"only whitespace", "   ", "", ""},
		{"single quote title", `path with spaces.jpg 'title'`, "path with spaces.jpg", `'title'`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotTarget, gotTitle := parseMarkdownLinkTargetLenient(tc.body)
			assert.Equal(t, tc.wantTarget, gotTarget)
			assert.Equal(t, tc.wantTitle, gotTitle)
		})
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
		"./images/demo.png": {ID: 42, Path: "images/demo.png"},
	}

	rewritten := rewriteMarkdownImageLinks(content, fileRefs, "share-token", "pwd")
	expected := `![demo](/api/share/file?id=42&share_token=share-token&password=pwd "title")`

	assert.Equal(t, expected, rewritten, "image links should be rewritten to share API URLs")
}

// TestRewriteMarkdownImageLinks_VideoAudio verifies that markdown image syntax
// pointing at a video or audio file is rewritten to a proper <video>/<audio>
// HTML element so the share view actually plays the media instead of trying
// to render it as a broken <img>.
// TestRewriteMarkdownImageLinks_VideoAudio 验证指向视频/音频文件的 Markdown
// 图片语法会被重写为 <video> / <audio> HTML 标签，使分享视图能正常播放。
func TestRewriteMarkdownImageLinks_VideoAudio(t *testing.T) {
	t.Run("video with chinese path", func(t *testing.T) {
		content := `![clip](视频/演示.mp4)`
		fileRefs := map[string]*domain.File{
			"视频/演示.mp4": {ID: 7, Path: "视频/演示.mp4"},
		}
		got := rewriteMarkdownImageLinks(content, fileRefs, "tok", "")
		expected := `<video src="/api/share/file?id=7&share_token=tok" controls style="max-width:100%"></video>`
		assert.Equal(t, expected, got)
	})
	t.Run("audio mp3", func(t *testing.T) {
		content := `![track](audio/song.mp3)`
		fileRefs := map[string]*domain.File{
			"audio/song.mp3": {ID: 11, Path: "audio/song.mp3"},
		}
		got := rewriteMarkdownImageLinks(content, fileRefs, "tok", "")
		expected := `<audio src="/api/share/file?id=11&share_token=tok" controls></audio>`
		assert.Equal(t, expected, got)
	})
	t.Run("image still uses markdown form", func(t *testing.T) {
		content := `![pic](images/demo.png)`
		fileRefs := map[string]*domain.File{
			"images/demo.png": {ID: 1, Path: "images/demo.png"},
		}
		got := rewriteMarkdownImageLinks(content, fileRefs, "tok", "")
		expected := `![pic](/api/share/file?id=1&share_token=tok)`
		assert.Equal(t, expected, got)
	})
}

// TestNormalizeAmbiguousMarkdownImages exercises the rewrite that turns
// `![alt](path with spaces)` into `![alt](<path with spaces>)` when the
// lenient parse resolves to a real file but the strict parse does not.
// TestNormalizeAmbiguousMarkdownImages 覆盖将 `![alt](path with spaces)`
// 改写为 `![alt](<path with spaces>)` 的逻辑。
func TestNormalizeAmbiguousMarkdownImages(t *testing.T) {
	t.Run("rewrites literal-space ref when only lenient resolves", func(t *testing.T) {
		fileLinks := map[string]string{"en space/img2.jpg": "en space/img2.jpg"}
		got := normalizeAmbiguousMarkdownImages(`![pic](en space/img2.jpg)`, func(r string) bool {
			_, ok := fileLinks[r]
			return ok
		})
		assert.Equal(t, `![pic](<en space/img2.jpg>)`, got)
	})

	t.Run("preserves title segment", func(t *testing.T) {
		fileLinks := map[string]string{"en space/img2.jpg": "en space/img2.jpg"}
		got := normalizeAmbiguousMarkdownImages(`![pic](en space/img2.jpg "demo")`, func(r string) bool {
			_, ok := fileLinks[r]
			return ok
		})
		assert.Equal(t, `![pic](<en space/img2.jpg> "demo")`, got)
	})

	t.Run("leaves strict-resolving refs alone", func(t *testing.T) {
		fileLinks := map[string]string{"images/demo.png": "images/demo.png"}
		input := `![pic](images/demo.png)`
		got := normalizeAmbiguousMarkdownImages(input, func(r string) bool {
			_, ok := fileLinks[r]
			return ok
		})
		assert.Equal(t, input, got)
	})

	t.Run("leaves angle-bracket form untouched", func(t *testing.T) {
		fileLinks := map[string]string{"en space/img2.jpg": "en space/img2.jpg"}
		input := `![pic](<en space/img2.jpg>)`
		got := normalizeAmbiguousMarkdownImages(input, func(r string) bool {
			_, ok := fileLinks[r]
			return ok
		})
		assert.Equal(t, input, got)
	})

	t.Run("does not rewrite when lenient also fails", func(t *testing.T) {
		input := `![pic](missing space/path.jpg)`
		got := normalizeAmbiguousMarkdownImages(input, func(string) bool { return false })
		assert.Equal(t, input, got)
	})

	t.Run("nil callback is a no-op", func(t *testing.T) {
		input := `![pic](en space/img2.jpg)`
		assert.Equal(t, input, normalizeAmbiguousMarkdownImages(input, nil))
	})
}
