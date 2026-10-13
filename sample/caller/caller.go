package caller

import "tui/sample/lib"

// Total 汇总一批数据的总量。
func Total(items []int) int {
	return lib.Compute(items)
}