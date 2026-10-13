package main

import "testing"

func TestEnvInt64(t *testing.T) {
	const key = "AI_REVIEW_TEST_INT64"
	t.Setenv(key, "")
	if got := envInt64(key, 123); got != 123 {
		t.Errorf("未设置时应返回默认值 123，实际 %d", got)
	}
	t.Setenv(key, "abc")
	if got := envInt64(key, 123); got != 123 {
		t.Errorf("非法值应返回默认值 123，实际 %d", got)
	}
	t.Setenv(key, "0")
	if got := envInt64(key, 123); got != 123 {
		t.Errorf("非正值应返回默认值 123，实际 %d", got)
	}
	t.Setenv(key, "2048")
	if got := envInt64(key, 123); got != 2048 {
		t.Errorf("合法值应被采用，实际 %d", got)
	}
}
