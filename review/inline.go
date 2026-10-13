package main

import (
	"regexp"
	"strconv"
	"strings"
)

// hunkHeaderRe 匹配 unified diff 的 hunk 头，捕获新文件侧的起始行号。
// 形如：@@ -12,7 +14,9 @@ func foo() {
var hunkHeaderRe = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// ParseDiffLines 解析 unified diff，返回「每个文件在变更后（新文件 / RIGHT 侧）出现的行号集合」。
// 集合中的行号（新增行 + 上下文行）是 GitHub 行内评论允许挂载的合法位置；
// 落在集合外的行号提交行内评论会被 GitHub 拒绝（422）。
func ParseDiffLines(diff string) map[string]map[int]bool {
	result := map[string]map[int]bool{}
	var currentFile string
	newLine := 0
	inHunk := false

	for _, ln := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(ln, "+++ "):
			// 进入新文件的 diff 头，重置状态
			currentFile = parseDiffPath(ln)
			inHunk = false
			if currentFile != "" && result[currentFile] == nil {
				result[currentFile] = map[int]bool{}
			}
		case strings.HasPrefix(ln, "@@"):
			// 开始新的 hunk，按新文件侧起始行号重置计数
			m := hunkHeaderRe.FindStringSubmatch(ln)
			if m == nil || currentFile == "" {
				inHunk = false
				continue
			}
			start, _ := strconv.Atoi(m[1])
			newLine = start
			inHunk = true
		case !inHunk:
			// hunk 之外的行（index、--- a/xxx 等）忽略
		case strings.HasPrefix(ln, "+"):
			result[currentFile][newLine] = true
			newLine++
		case strings.HasPrefix(ln, "-"):
			// 删除行不占用新文件行号
		case strings.HasPrefix(ln, " "):
			result[currentFile][newLine] = true
			newLine++
		case strings.HasPrefix(ln, `\`):
			// "\ No newline at end of file"，忽略
		}
	}
	return result
}

// parseDiffPath 从 "+++ b/path" 行提取文件路径。
func parseDiffPath(line string) string {
	p := strings.TrimSpace(strings.TrimPrefix(line, "+++ "))
	if i := strings.IndexByte(p, '\t'); i != -1 {
		p = p[:i]
	}
	if p == "/dev/null" {
		return ""
	}
	return strings.TrimPrefix(p, "b/")
}

// SplitComments 按行号合法性把评论分为可挂载到代码行的（inline）与无法定位的（orphan）。
func SplitComments(comments []Comment, valid map[string]map[int]bool) (inline, orphan []Comment) {
	for _, c := range comments {
		if lines, ok := valid[c.File]; ok && lines[c.Line] {
			inline = append(inline, c)
			continue
		}
		orphan = append(orphan, c)
	}
	return inline, orphan
}