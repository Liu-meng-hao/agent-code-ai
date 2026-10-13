package main

import "testing"

func TestParseDiffLines(t *testing.T) {
	diff := `diff --git a/a.go b/a.go
index 1111111..2222222 100644
--- a/a.go
+++ b/a.go
@@ -10,3 +10,5 @@ func foo() {
 	line10
+	line11
+	line12
 	line13
`
	valid := ParseDiffLines(diff)
	lines, ok := valid["a.go"]
	if !ok {
		t.Fatalf("未解析出 a.go 的行号集合: %v", valid)
	}
	// 新文件侧应为 10..13（含上下文行与新增行）
	for _, n := range []int{10, 11, 12, 13} {
		if !lines[n] {
			t.Errorf("期望行号 %d 合法，实际不在集合中", n)
		}
	}
	for _, n := range []int{9, 14, 0} {
		if lines[n] {
			t.Errorf("行号 %d 不应合法", n)
		}
	}
}

func TestParseDiffLinesDeletedFile(t *testing.T) {
	// 删除文件时新文件侧为 /dev/null，不应产生任何行号集合
	diff := `diff --git a/gone.go b/gone.go
deleted file mode 100644
--- a/gone.go
+++ /dev/null
@@ -1,2 +0,0 @@
-package main
-func main() {}
`
	valid := ParseDiffLines(diff)
	if len(valid) != 0 {
		t.Errorf("删除文件不应产生合法行号，实际: %v", valid)
	}
}

func TestSplitComments(t *testing.T) {
	valid := map[string]map[int]bool{
		"a.go": {10: true, 11: true},
	}
	comments := []Comment{
		{File: "a.go", Line: 11}, // 可挂载
		{File: "a.go", Line: 99}, // 行号越界
		{File: "b.go", Line: 1},  // 文件不在变更列表
	}
	inline, orphan := SplitComments(comments, valid)
	if len(inline) != 1 || inline[0].Line != 11 {
		t.Errorf("期望 1 条行内评论(line=11)，实际: %+v", inline)
	}
	if len(orphan) != 2 {
		t.Errorf("期望 2 条无法定位评论，实际: %+v", orphan)
	}
}

func TestParseDiffPath(t *testing.T) {
	cases := map[string]string{
		"+++ b/review/main.go":     "review/main.go",
		"+++ b/path with space.go": "path with space.go",
		"+++ /dev/null":            "",
	}
	for in, want := range cases {
		if got := parseDiffPath(in); got != want {
			t.Errorf("parseDiffPath(%q) = %q, 期望 %q", in, got, want)
		}
	}
}
