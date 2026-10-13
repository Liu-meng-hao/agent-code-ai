package lib

// Compute 返回切片中所有元素之和。
func Compute(items []int) int {
	sum := 0
	for _, v := range items {
		sum += v
	}
	return sum
}