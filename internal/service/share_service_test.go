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

// TestRewriteMarkdownImageLinks_VideoDispatch verifies that markdown image
// syntax pointing at a video file is rewritten to a <video> HTML element
// instead of staying as a (broken) ![alt](url) image.
//
// TestRewriteMarkdownImageLinks_VideoDispatch 验证指向视频文件的 Markdown
// 图片语法会被重写为 <video> HTML 标签，而不是保持为（坏掉的）![alt](url)。
func TestRewriteMarkdownImageLinks_VideoDispatch(t *testing.T) {
	content := `![demo clip](videos/demo.mp4)`
	fileRefs := map[string]*domain.File{
		"videos/demo.mp4": {ID: 99, Path: "videos/demo.mp4"},
	}

	rewritten := rewriteMarkdownImageLinks(content, fileRefs, "tk", "")

	assert.Contains(t, rewritten, "<video")
	assert.Contains(t, rewritten, "controls")
	assert.Contains(t, rewritten, "/api/share/file?id=99&share_token=tk")
	assert.NotContains(t, rewritten, "![demo clip]")
}

// TestRewriteMarkdownImageLinks_AudioDispatch verifies that markdown image
// syntax pointing at an audio file is rewritten to an <audio> HTML element.
//
// TestRewriteMarkdownImageLinks_AudioDispatch 验证指向音频文件的 Markdown
// 图片语法会被重写为 <audio> HTML 标签。
func TestRewriteMarkdownImageLinks_AudioDispatch(t *testing.T) {
	content := `![bgm](audio/song.mp3)`
	fileRefs := map[string]*domain.File{
		"audio/song.mp3": {ID: 100, Path: "audio/song.mp3"},
	}

	rewritten := rewriteMarkdownImageLinks(content, fileRefs, "tk", "pw")

	assert.Contains(t, rewritten, "<audio")
	assert.Contains(t, rewritten, "controls")
	assert.Contains(t, rewritten, "/api/share/file?id=100&share_token=tk&password=pw")
	assert.NotContains(t, rewritten, "![bgm]")
}


// TestExtractSharedNoteFileRefs_HTMLMedia verifies that <video>, <audio> and
// <source> tags with a `src` attribute are also extracted into the shared
// file ref list, mirroring the existing <img> behavior. Without this the
// share authorization map never learns about media files referenced from
// inline HTML, and the share viewer's embedded player cannot fetch them.
//
// TestExtractSharedNoteFileRefs_HTMLMedia 验证 <video>/<audio>/<source> 标签
// 的 src 也会被提取到分享文件引用列表中（行为与 <img> 一致）。否则分享授权
// 映射根本不会包含这些媒体文件，分享视图里的播放器也就拿不到对应资源。
func TestExtractSharedNoteFileRefs_HTMLMedia(t *testing.T) {
	content := `
<video src="assets/clip.mp4" controls width="500"></video>
<audio src="assets/song.mp3" controls></audio>
<video controls>
  <source src="assets/clip.webm" type="video/webm">
  <source src="assets/clip.mp4" type="video/mp4" />
</video>
<VIDEO src="assets/upper.mp4"></VIDEO>
<video src="https://cdn.example.com/remote.mp4"></video>
`
	refs := extractSharedNoteFileRefs(content)

	expected := map[string]struct{}{
		"assets/clip.mp4":  {},
		"assets/song.mp3":  {},
		"assets/clip.webm": {},
		"assets/upper.mp4": {},
	}

	assert.Len(t, refs, len(expected), "should extract exactly %d media refs", len(expected))
	for _, ref := range refs {
		_, ok := expected[ref]
		assert.True(t, ok, "unexpected ref extracted: %q", ref)
	}

	// Remote URLs must be rejected by isLocalSharePath.
	for _, ref := range refs {
		assert.NotContains(t, ref, "cdn.example.com", "remote URL must not be extracted")
	}
}

// TestExtractSharedNoteFileRefs_HTMLMediaSpacesAndCJK verifies that media
// paths containing literal spaces and non-ASCII characters (Chinese in this
// case) are extracted unchanged, matching how the WebGUI fileLinks lookup
// keys the table by raw ref form.
//
// TestExtractSharedNoteFileRefs_HTMLMediaSpacesAndCJK 验证含字面空格与非
// ASCII 字符（这里是中文）的媒体路径会被原样提取，与 WebGUI 端按原始引用
// 形式查 fileLinks 的逻辑保持一致。
func TestExtractSharedNoteFileRefs_HTMLMediaSpacesAndCJK(t *testing.T) {
	content := `<video src="视频/演 示.mp4" controls></video>` +
		`<audio src='音频/背景 音乐.mp3'></audio>`
	refs := extractSharedNoteFileRefs(content)

	expected := map[string]struct{}{
		"视频/演 示.mp4":      {},
		"音频/背景 音乐.mp3": {},
	}

	assert.Len(t, refs, len(expected))
	for _, ref := range refs {
		_, ok := expected[ref]
		assert.True(t, ok, "unexpected ref extracted: %q", ref)
	}
}

// TestRewriteHTMLMediaSources_Video verifies that rewriteHTMLMediaSources
// swaps the `src` attribute on a <video> tag for the share API URL while
// preserving the surrounding attributes verbatim.
//
// TestRewriteHTMLMediaSources_Video 验证 rewriteHTMLMediaSources 会把 <video>
// 的 src 替换为分享 API URL，同时原样保留其他属性。
func TestRewriteHTMLMediaSources_Video(t *testing.T) {
	content := `<video src="assets/clip.mp4" controls width="500"></video>`
	fileRefs := map[string]*domain.File{
		"assets/clip.mp4": {ID: 7, Path: "assets/clip.mp4"},
	}

	rewritten := rewriteHTMLMediaSources(content, htmlVideoRegex, "video", fileRefs, "tk", "pw")

	expected := `<video src="/api/share/file?id=7&share_token=tk&password=pw" controls width="500"></video>`
	assert.Equal(t, expected, rewritten)
}

// TestRewriteHTMLMediaSources_Audio mirrors TestRewriteHTMLMediaSources_Video
// for <audio>.
//
// TestRewriteHTMLMediaSources_Audio 与 TestRewriteHTMLMediaSources_Video 类似，
// 验证 <audio> 标签的 src 改写。
func TestRewriteHTMLMediaSources_Audio(t *testing.T) {
	content := `<audio src='assets/song.mp3' controls></audio>`
	fileRefs := map[string]*domain.File{
		"assets/song.mp3": {ID: 8, Path: "assets/song.mp3"},
	}

	rewritten := rewriteHTMLMediaSources(content, htmlAudioRegex, "audio", fileRefs, "tk", "")

	expected := `<audio src='/api/share/file?id=8&share_token=tk' controls></audio>`
	assert.Equal(t, expected, rewritten)
}

// TestRewriteHTMLMediaSources_NestedSourceSelfClosing verifies that nested
// <source> children of <video>/<audio> are rewritten correctly, including
// the self-closing `<source ... />` form. The trailing `/` is captured in
// the post-src group of the regex and survives into the replacement.
//
// TestRewriteHTMLMediaSources_NestedSourceSelfClosing 验证嵌套在 <video>/<audio>
// 内的 <source> 子标签也能被正确改写，包括 `<source ... />` 自闭合形式；尾部
// 的 `/` 由正则的 src 之后捕获组保留并直接进入替换结果。
func TestRewriteHTMLMediaSources_NestedSourceSelfClosing(t *testing.T) {
	content := `<video controls>` +
		`<source src="assets/clip.webm" type="video/webm">` +
		`<source src="assets/clip.mp4" type="video/mp4" />` +
		`</video>`
	fileRefs := map[string]*domain.File{
		"assets/clip.webm": {ID: 11, Path: "assets/clip.webm"},
		"assets/clip.mp4":  {ID: 12, Path: "assets/clip.mp4"},
	}

	rewritten := rewriteHTMLMediaSources(content, htmlSourceRegex, "source", fileRefs, "tk", "")

	expected := `<video controls>` +
		`<source src="/api/share/file?id=11&share_token=tk" type="video/webm">` +
		`<source src="/api/share/file?id=12&share_token=tk" type="video/mp4" />` +
		`</video>`
	assert.Equal(t, expected, rewritten)
}

// TestRewriteHTMLMediaSources_RemoteURLUntouched verifies that media tags
// pointing at fully qualified remote URLs are left untouched (the remote
// URL is rejected by isLocalSharePath upstream so it never enters fileRefs,
// causing the rewriter to fall through with the original match).
//
// TestRewriteHTMLMediaSources_RemoteURLUntouched 验证指向远程 URL 的媒体标签
// 会被原样保留：上游 isLocalSharePath 会拒绝远程地址，因此 fileRefs 中没有
// 对应键，重写器命中不到、直接返回原匹配。
func TestRewriteHTMLMediaSources_RemoteURLUntouched(t *testing.T) {
	content := `<video src="https://cdn.example.com/clip.mp4" controls></video>`
	rewritten := rewriteHTMLMediaSources(content, htmlVideoRegex, "video", map[string]*domain.File{}, "tk", "")
	assert.Equal(t, content, rewritten)
}

// TestRewriteHTMLMediaSources_MixedCase verifies case-insensitive matching:
// authors who wrote `<VIDEO>` in capital letters get the same rewrite, and
// the output normalizes the tag name to the lowercase form passed via the
// `tagName` argument (consistent with HTML semantics where tag names are
// case-insensitive).
//
// TestRewriteHTMLMediaSources_MixedCase 验证大小写不敏感匹配：作者把
// `<VIDEO>` 写成大写时也能改写，并且输出按 `tagName` 参数统一为小写形式
// （HTML 标签名本就大小写无关）。
func TestRewriteHTMLMediaSources_MixedCase(t *testing.T) {
	content := `<VIDEO src="assets/clip.mp4" controls></VIDEO>`
	fileRefs := map[string]*domain.File{
		"assets/clip.mp4": {ID: 21, Path: "assets/clip.mp4"},
	}

	rewritten := rewriteHTMLMediaSources(content, htmlVideoRegex, "video", fileRefs, "tk", "")

	expected := `<video src="/api/share/file?id=21&share_token=tk" controls></VIDEO>`
	assert.Equal(t, expected, rewritten)
}
